// Package picks implements the pick state machine and exports picked files
// into <scanRoot>/<picksDir>/. It depends only on the store and the filesystem.
package picks

import (
	"errors"
	"fmt"

	"github.com/volodymyrsmirnov/choix/internal/store"
)

// State enumerates the persisted pick states. Values match the strings stored
// in the picks.state column.
type State string

const (
	StatePicked   State = "picked"
	StateRejected State = "rejected"
)

// Service implements the pick state machine. It is safe for serial use; the
// caller is responsible for any cross-call synchronization.
type Service struct {
	store    *store.Store
	scanRoot string
	picksDir string
}

// New constructs a Service. scanRoot is the absolute path to the user's media
// folder; picksDir is the relative directory under scanRoot where picks are
// copied (typically "picks").
func New(s *store.Store, scanRoot, picksDir string) *Service {
	return &Service{store: s, scanRoot: scanRoot, picksDir: picksDir}
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

// Pick transitions the file to StatePicked and exports it. Idempotent: a second
// Pick on an already-picked file is a no-op. Auto-export happens inside this
// call; callers do not invoke Export directly.
func (s *Service) Pick(fileID int64) error {
	f, err := s.fileFor(fileID)
	if err != nil {
		return err
	}
	current, _ := s.store.Picks().Get(fileID)
	if current.State == string(StatePicked) {
		return nil
	}
	if err := s.store.Picks().SetState(fileID, string(StatePicked)); err != nil {
		return fmt.Errorf("picks: set state picked for %d: %w", fileID, err)
	}
	rel, err := s.exportLocked(f)
	if err != nil {
		// Roll back state to keep DB consistent with disk.
		_ = s.store.Picks().Delete(fileID)
		return fmt.Errorf("picks: export %d: %w", fileID, err)
	}
	if err := s.store.Picks().SetExportedPath(fileID, rel); err != nil {
		return fmt.Errorf("picks: persist exported_path for %d: %w", fileID, err)
	}
	return nil
}

// Unpick removes the picked state and unexports the file.
func (s *Service) Unpick(fileID int64) error {
	if _, err := s.fileFor(fileID); err != nil {
		return err
	}
	current, err := s.store.Picks().Get(fileID)
	if err != nil {
		return nil // already unmarked
	}
	if current.State != string(StatePicked) {
		return nil
	}
	if err := s.unexportLocked(fileID); err != nil {
		return fmt.Errorf("picks: unexport %d: %w", fileID, err)
	}
	if err := s.store.Picks().Delete(fileID); err != nil {
		return fmt.Errorf("picks: delete pick row %d: %w", fileID, err)
	}
	return nil
}

// Reject transitions the file to StateRejected. If it was previously picked,
// the exported file is removed.
func (s *Service) Reject(fileID int64) error {
	if _, err := s.fileFor(fileID); err != nil {
		return err
	}
	current, _ := s.store.Picks().Get(fileID)
	if current.State == string(StatePicked) {
		if err := s.unexportLocked(fileID); err != nil {
			return fmt.Errorf("picks: unexport before reject %d: %w", fileID, err)
		}
	}
	if err := s.store.Picks().SetState(fileID, string(StateRejected)); err != nil {
		return fmt.Errorf("picks: set state rejected for %d: %w", fileID, err)
	}
	return s.store.Picks().ClearExportedPath(fileID)
}

// Unreject clears a rejected mark, returning the file to unmarked.
func (s *Service) Unreject(fileID int64) error {
	if _, err := s.fileFor(fileID); err != nil {
		return err
	}
	current, err := s.store.Picks().Get(fileID)
	if err != nil {
		return nil
	}
	if current.State != string(StateRejected) {
		return nil
	}
	return s.store.Picks().Delete(fileID)
}

// exportLocked / unexportLocked are implemented in export.go.
// They are package-private — only Pick/Unpick/Reject call them.

func (s *Service) exportLocked(f store.File) (string, error) { return s.exportFile(f) }
func (s *Service) unexportLocked(fileID int64) error         { return s.Unexport(fileID) }
