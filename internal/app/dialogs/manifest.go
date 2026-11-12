package dialogs

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
)

// The roles a manifest button may have (frontend/src/design/dialogs.ts).
const (
	RoleDefault     = "default"     // answers Return; at most one
	RoleCancel      = "cancel"      // answers Escape; exactly one
	RoleDestructive = "destructive" // neither
)

// maxButtons is the most buttons an NSAlert shows; Wails drops the rest.
const maxButtons = 4

// removeFolder is the manifest's "Remove from Library" alert.
const removeFolder = "remove-folder"

// The ways a manifest can fail ParseAlerts.
var (
	errUnknownRole    = errors.New("unknown button role")
	errNoCancel       = errors.New("no button with role cancel")
	errManyCancel     = errors.New("more than one button with role cancel")
	errManyDefault    = errors.New("more than one button with role default")
	errTooManyButtons = fmt.Errorf("more than %d buttons", maxButtons)
)

// Button is one button of an alert: its label and its role.
type Button struct{ Label, Role string }

// Alert is one native alert's approved copy. Title and Message may contain
// the placeholder {name}; Buttons are in NSAlert order, the first trailing.
type Alert struct {
	Title, Message string
	Buttons        []Button
}

// ParseAlerts parses the alert manifest (frontend/src/design/dialogs.json)
// into its alerts by id. It refuses a manifest the native alert could not
// show as approved: a button with an unknown role, an alert with no cancel
// button or with more than one, more than one default button, or more than
// four buttons.
func ParseAlerts(data []byte) (map[string]Alert, error) {
	var raw map[string]struct {
		Title   string `json:"title"`
		Message string `json:"message"`
		Buttons []struct {
			Label string `json:"label"`
			Role  string `json:"role"`
		} `json:"buttons"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("dialogs: parse manifest: %w", err)
	}
	alerts := make(map[string]Alert, len(raw))
	for id, r := range raw {
		a := Alert{Title: r.Title, Message: r.Message, Buttons: make([]Button, 0, len(r.Buttons))}
		for _, b := range r.Buttons {
			a.Buttons = append(a.Buttons, Button{Label: b.Label, Role: b.Role})
		}
		if err := a.validate(); err != nil {
			return nil, fmt.Errorf("dialogs: manifest alert %q: %w", id, err)
		}
		alerts[id] = a
	}
	return alerts, nil
}

// validate reports why the alert cannot be shown as approved, if it cannot.
func (a Alert) validate() error {
	var defaults, cancels int
	for i, b := range a.Buttons {
		switch b.Role {
		case RoleDefault:
			defaults++
		case RoleCancel:
			cancels++
		case RoleDestructive:
		default:
			return fmt.Errorf("button %d (%q): %w %q", i, b.Label, errUnknownRole, b.Role)
		}
	}
	switch {
	case len(a.Buttons) > maxButtons:
		return fmt.Errorf("%d buttons: %w", len(a.Buttons), errTooManyButtons)
	case cancels == 0:
		return errNoCancel
	case cancels > 1:
		return errManyCancel
	case defaults > 1:
		return errManyDefault
	}
	return nil
}

// For returns the alert for the item called name: {name} in the title and the
// message is replaced by name, once (a name that contains {name} is not
// expanded again) and unshortened. The buttons are a copy.
func (a Alert) For(name string) Alert {
	return Alert{
		Title:   strings.ReplaceAll(a.Title, "{name}", name),
		Message: strings.ReplaceAll(a.Message, "{name}", name),
		Buttons: slices.Clone(a.Buttons),
	}
}

// spec is the plain description a native alert is made from: its text, its
// button labels in manifest order, and the indexes of the default button (-1
// when there is none) and of the cancel button.
type spec struct {
	title, message string
	buttons        []string
	defaultButton  int
	cancelButton   int
}

// build describes the alert a as the native alert shows it. a is valid
// (ParseAlerts).
func build(a Alert) spec {
	s := spec{
		title:         a.Title,
		message:       a.Message,
		buttons:       make([]string, 0, len(a.Buttons)),
		defaultButton: -1,
		cancelButton:  -1,
	}
	for i, b := range a.Buttons {
		s.buttons = append(s.buttons, b.Label)
		switch b.Role {
		case RoleDefault:
			s.defaultButton = i
		case RoleCancel:
			s.cancelButton = i
		}
	}
	return s
}
