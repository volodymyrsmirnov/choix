// Package picks implements the pick state machine and exports picked files
// into <scanRoot>/<picksDir>/.
//
// Every write goes through internal/safefs: the export directory is opened
// component by component (a symlink anywhere is refused), the copy is written
// under a random temporary name, synced, and published exclusively, so an
// existing file is never overwritten. The state change is then committed with
// store.Durable. A failure before that commit leaves the state unchanged and
// removes a copy this call published; once the commit succeeds the state is
// never lost, and removing a copy afterwards (unpick, reject) is best effort.
package picks

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync/atomic"

	"github.com/volodymyrsmirnov/choix/internal/safefs"
	"github.com/volodymyrsmirnov/choix/internal/store"
)

// State is a file's pick state. Values match the strings stored in the
// picks.state column; StateNone (unmarked) has no row.
type State string

const (
	StateNone     State = ""
	StatePicked   State = "picked"
	StateRejected State = "rejected"
)

// Result is a file's pick state after Set, with the path (relative to the
// scan root) of its exported copy when it is picked.
type Result struct {
	State        State
	ExportedPath string
}

// Service implements the pick state machine for one folder. It is not safe
// for concurrent use: the caller serialises the mutations of a folder.
type Service struct {
	store    *store.Store
	scanRoot string
	picksDir string
}

// New constructs a Service. scanRoot is the absolute path to the user's media
// folder; picksDir is the directory, relative to scanRoot, where picks are
// copied (the folder's EffectiveDir). picksDir is validated when a pick is
// made, not here.
func New(s *store.Store, scanRoot, picksDir string) *Service {
	return &Service{store: s, scanRoot: scanRoot, picksDir: picksDir}
}

// Set moves fileID to target and returns the resulting state. Setting the
// current state is a no-op.
//
//   - To StatePicked: ValidateDir and CheckDir must pass; the original is
//     opened read-only and copied into <picksDir>/<dir of its path>/ under its
//     base name (an identical file already there is reused; a different one
//     gets _2, _3…); directories created on the way, the copy and its
//     directory are synced; then the state is committed durably. If that
//     commit fails, a copy this call published is removed.
//   - From StatePicked to StateRejected or StateNone: the state is committed
//     durably first, then the exported copy is removed and its directory
//     synced — unless, decided inside that transaction, the path is known as
//     an original (any scan_status), another pick row records the same copy,
//     or a lookup fails. A copy that cannot or may not be removed is logged
//     and left behind; the state change stands.
//   - Between StateRejected and StateNone: only the durable commit.
func (s *Service) Set(ctx context.Context, fileID int64, target State) (Result, error) {
	switch target {
	case StateNone, StatePicked, StateRejected:
	default:
		return Result{}, fmt.Errorf("picks: unknown state %q", target)
	}
	f, err := s.fileFor(fileID)
	if err != nil {
		return Result{}, err
	}
	cur, exported, err := s.current(fileID)
	if err != nil {
		return Result{}, err
	}
	if cur == target {
		return Result{State: cur, ExportedPath: exported}, nil
	}
	switch {
	case target == StatePicked:
		return s.pick(ctx, f)
	case cur == StatePicked:
		rel := removalCandidate(f, exported)
		remove := false
		err := s.commitWith(ctx, fileID, target, "", func(tx *sql.Tx) {
			if rel != "" {
				remove = s.removable(tx, rel, fileID)
			}
		})
		if err != nil {
			return Result{}, err
		}
		if remove {
			s.removeExport(rel)
		}
		return Result{State: target}, nil
	default:
		if err := s.commit(ctx, fileID, target, ""); err != nil {
			return Result{}, err
		}
		return Result{State: target}, nil
	}
}

// pick exports f into the picks directory and records it as picked.
func (s *Service) pick(ctx context.Context, f store.File) (Result, error) {
	if err := ValidateDir(s.picksDir); err != nil {
		return Result{}, err
	}
	dir := cleanDir(s.picksDir)
	if err := CheckDir(s.store, dir); err != nil {
		return Result{}, err
	}
	exp, err := s.export(ctx, f, dir)
	if err != nil {
		return Result{}, fmt.Errorf("picks: export %s: %w", f.Path, err)
	}
	defer func() { _ = exp.dir.Close() }()

	if err := s.commit(ctx, f.ID, StatePicked, exp.path); err != nil {
		if exp.published {
			s.discardCopy(exp)
		}
		return Result{}, err
	}
	return Result{State: StatePicked, ExportedPath: exp.path}, nil
}

// commit durably records state (and the exported path) for fileID.
func (s *Service) commit(ctx context.Context, fileID int64, state State, exportedPath string) error {
	return s.commitWith(ctx, fileID, state, exportedPath, nil)
}

// commitWith is commit that also runs then (when not nil) inside the same
// transaction, after the row is written; then cannot fail the commit.
func (s *Service) commitWith(ctx context.Context, fileID int64, state State, exportedPath string, then func(tx *sql.Tx)) error {
	trace("tx-commit")
	err := s.store.Durable(ctx, func(tx *sql.Tx) error {
		if err := s.store.Picks().SetTx(tx, fileID, string(state), exportedPath); err != nil {
			return err
		}
		if then != nil {
			then(tx)
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("picks: record %q for file %d: %w", state, fileID, err)
	}
	return nil
}

// fileFor loads the file row, returning a friendly error if missing.
func (s *Service) fileFor(fileID int64) (store.File, error) {
	f, err := s.store.Files().GetByID(fileID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return store.File{}, fmt.Errorf("picks: file id %d not found", fileID)
		}
		return store.File{}, fmt.Errorf("picks: load file %d: %w", fileID, err)
	}
	return f, nil
}

// current returns fileID's stored state and exported path.
func (s *Service) current(fileID int64) (State, string, error) {
	p, err := s.store.Picks().Get(fileID)
	if errors.Is(err, store.ErrNotFound) {
		return StateNone, "", nil
	}
	if err != nil {
		return "", "", fmt.Errorf("picks: load state of file %d: %w", fileID, err)
	}
	return State(p.State), p.ExportedPath.String, nil
}

// --- test seam ----------------------------------------------------------------

var traceFn atomic.Pointer[func(event string)]

// trace records a step of Set for tests ("copy", "remove", "tx-commit"; the
// safefs steps "file-sync", "rename", "dir-sync" arrive through safefs).
func trace(step string) {
	if fn := traceFn.Load(); fn != nil {
		(*fn)(step)
	}
}

// SetTraceForTest is for tests only. It makes fn receive, in order, every
// step Set performs: picks' own "copy", "remove" and "tx-commit" (emitted
// just before the step), and safefs's "file-sync", "rename" and "dir-sync",
// forwarded by installing safefs.Hooks{Trace: fn}. That replaces any safefs
// hooks already set; a test that also injects failures calls
// safefs.SetHooksForTest with Trace set to the same fn after this, and
// restores in reverse order. The returned restore reinstates the previous
// trace and safefs hooks. Process-wide: such tests must not run in parallel.
func SetTraceForTest(fn func(event string)) (restore func()) {
	prev := traceFn.Swap(&fn)
	restoreFS := safefs.SetHooksForTest(safefs.Hooks{Trace: fn})
	return func() {
		restoreFS()
		traceFn.Store(prev)
	}
}
