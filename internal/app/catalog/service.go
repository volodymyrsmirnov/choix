package catalog

import (
	"github.com/volodymyrsmirnov/choix/internal/app/apperr"
	"github.com/volodymyrsmirnov/choix/internal/app/opgate"
)

// Service is the catalog binding. Every exported method is a frontend
// binding; nothing else may be exported on it. Each enters the admission
// gate (an unavailable error after quit began) and returns an
// *apperr.Error. Operations that need a context run under the runtime's
// (Deps.Context).
type Service struct {
	c    *Catalog
	gate *opgate.Gate
}

// NewService returns the catalog binding over c. Every call enters g.
func NewService(c *Catalog, g *opgate.Gate) *Service {
	return &Service{c: c, gate: g}
}

// Open opens the folder and returns its cluster grid (Catalog.Open).
func (s *Service) Open(folderID string) (FolderView, error) {
	leave, err := s.gate.Enter()
	if err != nil {
		return FolderView{}, err
	}
	defer leave()
	v, err := s.c.Open(s.c.ctx, folderID)
	return v, bindingError(err)
}

// Close closes the folder when it is open (Catalog.Close).
func (s *Service) Close(folderID string) error {
	leave, err := s.gate.Enter()
	if err != nil {
		return err
	}
	defer leave()
	s.c.Close(folderID)
	return nil
}

// Clusters returns the open folder's cluster grid.
func (s *Service) Clusters(folderID string) (FolderView, error) {
	leave, err := s.gate.Enter()
	if err != nil {
		return FolderView{}, err
	}
	defer leave()
	v, err := s.c.Clusters(folderID)
	return v, bindingError(err)
}

// Cluster returns the focus payload of one cluster of the open folder.
func (s *Service) Cluster(folderID string, clusterID int64) (FocusResponse, error) {
	leave, err := s.gate.Enter()
	if err != nil {
		return FocusResponse{}, err
	}
	defer leave()
	r, err := s.c.Cluster(folderID, clusterID)
	return r, bindingError(err)
}

// SetPick applies pick, unpick, reject or unreject to a file of the open
// folder and returns once it is durably committed.
func (s *Service) SetPick(folderID string, fileID int64, action string) (PickResult, error) {
	leave, err := s.gate.Enter()
	if err != nil {
		return PickResult{}, err
	}
	defer leave()
	r, err := s.c.SetPick(folderID, fileID, action)
	return r, bindingError(err)
}

// Undo reverts the open folder's newest pick change.
func (s *Service) Undo(folderID string) (PickResult, error) {
	leave, err := s.gate.Enter()
	if err != nil {
		return PickResult{}, err
	}
	defer leave()
	r, err := s.c.Undo(folderID)
	return r, bindingError(err)
}

// Recluster rebuilds the open folder's clusters.
func (s *Service) Recluster(folderID string) error {
	leave, err := s.gate.Enter()
	if err != nil {
		return err
	}
	defer leave()
	return bindingError(s.c.Recluster(s.c.ctx, folderID))
}

// Ratings returns the open folder's quality ratings by decimal file id.
func (s *Service) Ratings(folderID string) (map[string]float64, error) {
	leave, err := s.gate.Enter()
	if err != nil {
		return nil, err
	}
	defer leave()
	r, err := s.c.Ratings(folderID)
	return r, bindingError(err)
}

// bindingError is err as the frontend receives it, or nil.
func bindingError(err error) error {
	if err == nil {
		return nil
	}
	return apperr.From(err)
}
