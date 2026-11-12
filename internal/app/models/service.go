package models

import (
	"fmt"

	"github.com/volodymyrsmirnov/choix/internal/ai/local"
	"github.com/volodymyrsmirnov/choix/internal/app/apperr"
	"github.com/volodymyrsmirnov/choix/internal/app/events"
	"github.com/volodymyrsmirnov/choix/internal/app/opgate"
)

// Field and reason of the invalid error Retry returns for an unknown kind.
const (
	fieldKind     = "kind"
	reasonUnknown = "unknown"
)

// qualityDisabled is the state Quality reports when the service has no
// quality source.
const qualityDisabled = "disabled"

// Service is the models binding: the models' download status, a retry of a
// failed download, and the quality rating's status. Every exported method
// is a frontend binding; nothing else may be exported on it. Each enters the
// admission gate first, so once quit began it answers unavailable ([call]
// C35).
type Service struct {
	m       *Manager
	quality func() events.Quality
	gate    *opgate.Gate
}

// NewService returns the models binding over m. quality returns the quality
// status (session.QualityTracker.Status at runtime); nil reports it
// disabled. Every call enters g.
func NewService(m *Manager, quality func() events.Quality, g *opgate.Gate) *Service {
	return &Service{m: m, quality: quality, gate: g}
}

// Status returns the state of every model, always [clip, quality].
func (s *Service) Status() ([]events.ModelStatus, error) {
	leave, err := s.gate.Enter()
	if err != nil {
		return nil, err
	}
	defer leave()
	return s.m.Status(), nil
}

// Retry queues a new round of download attempts for the model kind, "clip"
// or "quality", when its download failed; otherwise it does nothing. Any
// other kind is an invalid error for kind.
func (s *Service) Retry(kind string) error {
	leave, err := s.gate.Enter()
	if err != nil {
		return err
	}
	defer leave()
	k, ok := local.ParseModelKind(kind)
	if !ok {
		return apperr.Invalid(fieldKind, reasonUnknown, fmt.Sprintf("models: unknown model kind %q", kind))
	}
	s.m.Retry(k)
	return nil
}

// Quality returns the quality rating's status.
func (s *Service) Quality() (events.Quality, error) {
	leave, err := s.gate.Enter()
	if err != nil {
		return events.Quality{}, err
	}
	defer leave()
	if s.quality == nil {
		return events.Quality{State: qualityDisabled}, nil
	}
	return s.quality(), nil
}
