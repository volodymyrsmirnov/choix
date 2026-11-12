package main

import (
	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/volodymyrsmirnov/choix/internal/app/events"
)

// init registers every event the runtime sends with its payload type, so
// the binding generator types them for the frontend and Wails checks each
// emitted value against it. The calls are direct, with constant arguments,
// as the generator requires. Payloads are emitted by value: Wails matches
// the type exactly, and a pointer would be refused.
func init() {
	application.RegisterEvent[events.LibraryChangedEvent](events.LibraryChanged)
	application.RegisterEvent[events.ScanProgressEvent](events.ScanProgress)
	application.RegisterEvent[events.ScanStateEvent](events.ScanState)
	application.RegisterEvent[events.Quality](events.QualityStatus)
	application.RegisterEvent[events.FolderChangedEvent](events.FolderChanged)
	application.RegisterEvent[events.MenuActionEvent](events.MenuAction)
}
