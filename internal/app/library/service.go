package library

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"

	"github.com/volodymyrsmirnov/choix/internal/app/apperr"
	"github.com/volodymyrsmirnov/choix/internal/app/events"
	"github.com/volodymyrsmirnov/choix/internal/app/opgate"
)

// JobStates reports what the job queue is doing with a folder: "scanning"
// with its latest progress (nil before the first report), "queued", or "".
// *session.Queue implements it.
type JobStates interface {
	State(folderID string) (state string, p *events.Progress)
}

// FolderPicker shows the native folder picker and returns the absolute paths
// chosen; none (and no error) when the user cancelled. It blocks until the
// dialog closes. Implemented by internal/app/dialogs and by test fakes.
type FolderPicker interface {
	PickFolders() ([]string, error)
}

// Confirmer asks the user, natively, to confirm removing the folder named
// folderName from the Library; false when they declined. It blocks until the
// dialog closes.
type Confirmer interface {
	ConfirmRemove(folderName string) (bool, error)
}

// ServiceDeps are what the Service works with. Library, Picker, Confirm,
// Enqueue and Gate are required. A nil Jobs reports no job for any folder; a
// nil PreviewURL gives every folder an empty preview strip; an empty Home
// abbreviates no path.
type ServiceDeps struct {
	Library *Library
	Jobs    JobStates
	// Enqueue queues work for the folder, a full pass when full is set.
	Enqueue func(id string, full bool) error
	Picker  FolderPicker
	Confirm Confirmer
	// Home is the user's home directory, shown as "~" in parent paths.
	Home string
	// PreviewURL returns the URL of the folder's n-th preview image at stamp.
	PreviewURL func(id string, n int, stamp int64) string
	Gate       *opgate.Gate
}

// Service is the Library binding. Every exported method is a frontend
// binding; nothing else may be exported on it. Each enters the admission
// gate first (an unavailable error once quit began) and returns an
// *apperr.Error. A binding that shows a native dialog leaves the gate while
// the dialog is up — quit runs on the main thread, which the dialog holds —
// and enters it again when the dialog returns: when quit began meanwhile it
// answers unavailable and changes nothing.
//
// Nothing here relies on the order of the Library's events: a stats event
// may arrive after its folder's removed event, so List reflects library.db
// at the time of the call.
type Service struct {
	d ServiceDeps
}

// NewService returns the Library binding over d.
func NewService(d ServiceDeps) *Service {
	return &Service{d: d}
}

// List returns every folder as the Library page shows it (Summarise):
// newest month first, undated folders last, then by first capture time
// descending; ties, the undated folders among them, newest added first. It
// never returns a nil slice without an error.
func (s *Service) List() ([]FolderSummary, error) {
	leave, err := s.d.Gate.Enter()
	if err != nil {
		return nil, err
	}
	defer leave()
	folders, err := s.d.Library.Folders()
	if err != nil {
		return nil, apperr.From(fmt.Errorf("library: list: %w", err))
	}
	// The month is the first capture time's, so ordering by that time alone
	// orders by month first.
	slices.SortStableFunc(folders, compareForList)
	out := make([]FolderSummary, 0, len(folders))
	for _, f := range folders {
		var state string
		var p *events.Progress
		if s.d.Jobs != nil {
			state, p = s.d.Jobs.State(f.ID)
		}
		out = append(out, Summarise(f, state, p, s.d.Home, s.d.PreviewURL))
	}
	return out, nil
}

// compareForList orders folders for List: dated before undated, first
// capture time descending, added_at descending, then id.
func compareForList(a, b Folder) int {
	af, bf := a.Stats.FirstCaptured, b.Stats.FirstCaptured
	switch {
	case af.Valid && !bf.Valid:
		return -1
	case !af.Valid && bf.Valid:
		return 1
	case af.Valid && af.Int64 != bf.Int64:
		return cmp.Compare(bf.Int64, af.Int64)
	case a.AddedAt != b.AddedAt:
		return cmp.Compare(b.AddedAt, a.AddedAt)
	}
	return cmp.Compare(a.ID, b.ID)
}

// AddFolders shows the folder picker and adds the folders chosen
// (Library.AddPaths), in order; each new folder is queued for a full pass.
// A cancelled picker gives an empty slice. A path that cannot be added is
// logged and left out; when no path could be added, the first failure is
// returned. A folder that was added but could not be queued stays added and
// in the results, and the first such failure is always returned with them
// (Rescan queues the folder later). The gate is left while the picker is up
// and entered again before anything is added.
func (s *Service) AddFolders() ([]AddResult, error) {
	leave, err := s.d.Gate.Enter()
	if err != nil {
		return nil, err
	}
	leave()

	paths, pickErr := s.d.Picker.PickFolders()

	leave, err = s.d.Gate.Enter()
	if err != nil {
		return nil, err
	}
	defer leave()
	if pickErr != nil {
		return nil, apperr.From(fmt.Errorf("library: pick folders: %w", pickErr))
	}
	if len(paths) == 0 {
		return []AddResult{}, nil
	}
	res, addErrs, enqueueErrs := s.d.Library.addPaths(paths, s.enqueueNew)
	for _, err := range addErrs {
		slog.Warn("library: add a picked folder", "err", err)
	}
	if len(enqueueErrs) > 0 {
		// The folders stay added (and in res); the frontend learns that one
		// was not queued, and Rescan starts it.
		return res, apperr.From(enqueueErrs[0])
	}
	if len(res) == 0 && len(addErrs) > 0 {
		return nil, apperr.From(addErrs[0])
	}
	return res, nil
}

// enqueueNew queues a full pass for a folder AddFolders added. A failure is
// logged and returned; the folder stays in the Library.
func (s *Service) enqueueNew(id string) error {
	if err := s.d.Enqueue(id, true); err != nil {
		slog.Warn("library: queue a new folder", "folder", id, "err", err)
		return fmt.Errorf("library: queue new folder %s: %w", id, err)
	}
	return nil
}

// Remove asks the user to confirm and then removes the folder from the
// Library (Library.Remove, whose OnRemoved cancels its job); nothing inside
// the folder is touched. It returns false when the user declined. An unknown
// folder is not_found and no dialog is shown; a folder that went away while
// the dialog was up counts as removed. The gate is left while the dialog is
// up and entered again before anything is removed.
func (s *Service) Remove(id string) (bool, error) {
	leave, err := s.d.Gate.Enter()
	if err != nil {
		return false, err
	}
	f, err := s.d.Library.Folder(id)
	leave()
	if err != nil {
		return false, apperr.From(fmt.Errorf("library: remove: %w", err))
	}

	ok, confirmErr := s.d.Confirm.ConfirmRemove(f.Name)

	leave, err = s.d.Gate.Enter()
	if err != nil {
		return false, err
	}
	defer leave()
	if confirmErr != nil {
		return false, apperr.From(fmt.Errorf("library: confirm removal: %w", confirmErr))
	}
	if !ok {
		return false, nil
	}
	if err := s.d.Library.Remove(id); err != nil {
		if errors.Is(err, ErrNotFound) {
			return true, nil
		}
		return false, apperr.From(err)
	}
	return true, nil
}

// Rescan queues a full pass for the folder. An offline folder is offline and
// nothing is queued; an unknown one is not_found.
func (s *Service) Rescan(id string) error {
	leave, err := s.d.Gate.Enter()
	if err != nil {
		return err
	}
	defer leave()
	f, err := s.d.Library.Folder(id)
	if err != nil {
		return apperr.From(fmt.Errorf("library: rescan: %w", err))
	}
	if f.Presence == presenceOffline {
		return apperr.From(fmt.Errorf("library: rescan %s: %w", id, ErrOffline))
	}
	if err := s.d.Enqueue(id, true); err != nil {
		return apperr.From(fmt.Errorf("library: rescan: %w", err))
	}
	return nil
}

// CheckNow checks the presence of every folder at once (CheckPresence).
func (s *Service) CheckNow() error {
	leave, err := s.d.Gate.Enter()
	if err != nil {
		return err
	}
	defer leave()
	if _, err := s.d.Library.CheckPresence(context.Background()); err != nil {
		return apperr.From(err)
	}
	return nil
}
