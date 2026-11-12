// Package models is a compatibility shim, to be deleted in Task 15 of the
// built-in grouping and quality plan (spec §9.3). Choix no longer downloads
// any model; the shim keeps the Models binding, with the same three methods,
// so the generated bindings the unchanged app code imports stay the same
// until that code moves to Catalog.QualityStatus. It has no manager and no
// downloader and emits no event.
package models

import (
	"fmt"

	"github.com/volodymyrsmirnov/choix/internal/app/apperr"
	"github.com/volodymyrsmirnov/choix/internal/app/events"
	"github.com/volodymyrsmirnov/choix/internal/app/opgate"
)

// Field and reason of the invalid error Retry returns for an unknown kind.
const (
	fieldKind     = "kind"
	reasonUnknown = "unknown"
)

// The two model kinds the app code still asks about, and the state the shim
// reports for both.
const (
	kindCLIP        = "clip"
	kindQuality     = "quality"
	stateReady      = "ready"
	qualityDisabled = "disabled" // Quality's state when the service has no source
)

// Service is the models binding: the models' download status, a retry of a
// failed download, and the quality rating's status. Every exported method
// is a frontend binding; nothing else may be exported on it. Each enters the
// admission gate first, so once quit began it answers unavailable ([call]
// C35).
type Service struct {
	// The doc comments of Service and its methods are those of the binding
	// before the shim: they are copied into the generated bindings, which
	// must stay byte-identical until the shim is deleted. What the shim does
	// is said in the package comment and in each method's body.
	quality func() events.Quality
	gate    *opgate.Gate
}

// NewService returns the shim. quality returns the quality status (the
// runtime passes session.QualityTracker.Status, the same callback the
// catalog's QualityStatus uses); nil reports it disabled. Every call enters
// g.
func NewService(quality func() events.Quality, g *opgate.Gate) *Service {
	return &Service{quality: quality, gate: g}
}

// Status returns the state of every model, always [clip, quality].
func (s *Service) Status() ([]events.ModelStatus, error) {
	// Shim: both are ready, since there is nothing to download.
	leave, err := s.gate.Enter()
	if err != nil {
		return nil, err
	}
	defer leave()
	return []events.ModelStatus{
		{Kind: kindCLIP, State: stateReady},
		{Kind: kindQuality, State: stateReady},
	}, nil
}

// Retry queues a new round of download attempts for the model kind, "clip"
// or "quality", when its download failed; otherwise it does nothing. Any
// other kind is an invalid error for kind.
func (s *Service) Retry(kind string) error {
	// Shim: no download ever fails, so a valid kind does nothing.
	leave, err := s.gate.Enter()
	if err != nil {
		return err
	}
	defer leave()
	if kind != kindCLIP && kind != kindQuality {
		return apperr.Invalid(fieldKind, reasonUnknown, fmt.Sprintf("models: unknown model kind %q", kind))
	}
	return nil
}

// Quality returns the quality rating's status.
func (s *Service) Quality() (events.Quality, error) {
	// Shim: the same snapshot as Catalog.QualityStatus.
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
