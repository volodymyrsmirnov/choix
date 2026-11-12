// Package apperr is the error the frontend receives from every service
// binding: a stable code it can switch on, a message, and for a refused
// input the field and a reason code. From turns any Go error into one:
// an *Error passes through, a sentinel registered with Register or
// RegisterInvalid gets the code and message registered for it, and
// anything else is internal. Marshal is the application's error marshaller
// (Wails' application.Options.MarshalError), so a service may return any
// error and the frontend still receives this shape.
//
// The package does not import Wails: cmd/choix passes Marshal to it.
package apperr

import (
	"encoding/json"
	"errors"
	"reflect"
	"sync"

	"github.com/volodymyrsmirnov/choix/internal/picks"
	"github.com/volodymyrsmirnov/choix/internal/safefs"
	"github.com/volodymyrsmirnov/choix/internal/store"
)

// Error codes. The frontend switches on these; they never change meaning.
const (
	CodeNotFound    = "not_found"   // the folder, file or cluster does not exist
	CodeNotOpen     = "not_open"    // the operation needs an open folder and none is
	CodeOffline     = "offline"     // the folder's volume is not mounted
	CodeAccessLost  = "access_lost" // the folder's bookmark no longer grants access
	CodeReadOnly    = "read_only"   // Choix cannot write in the folder
	CodeInvalid     = "invalid"     // the input was refused; see Field and Reason
	CodeUnavailable = "unavailable" // Choix is shutting down (the closed gate); nothing else
	CodeBusy        = "busy"        // not possible right now, but Choix is not quitting: try again shortly
	CodeInternal    = "internal"    // anything else; Message is the Go error text
)

// Error is what a service binding returns to the frontend. Field names the
// refused input (a settings key such as "picks_dir") and Reason says why,
// as a code the frontend maps to its own copy; both are empty for errors
// that are not about one input.
type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Field   string `json:"field,omitempty"`
	Reason  string `json:"reason,omitempty"`
}

// Error returns the message, or the code when the message is empty.
func (e *Error) Error() string {
	if e.Message == "" {
		return e.Code
	}
	return e.Message
}

// New returns an error with code and message.
func New(code, message string) *Error {
	return &Error{Code: code, Message: message}
}

// Invalid returns an invalid-input error for field with a reason code. The
// message is for logs; the frontend shows its own copy for the reason.
func Invalid(field, reason, message string) *Error {
	return &Error{Code: CodeInvalid, Message: message, Field: field, Reason: reason}
}

// registration is one sentinel and the error From reports for it.
type registration struct {
	target error
	as     Error
}

var (
	registryMu sync.RWMutex
	registry   []registration
)

// Register maps the sentinel target, wherever it appears in an error's
// chain (errors.Is), to code and message; when a chain holds several
// registered sentinels, the outermost wins (see From). An empty message
// means target's own text. Registering target again replaces the earlier
// registration.
func Register(target error, code, message string) {
	register(target, Error{Code: code, Message: message})
}

// RegisterInvalid maps the sentinel target to an invalid-input error with
// field and reason (either may be empty). An empty message means target's
// own text.
func RegisterInvalid(target error, field, reason, message string) {
	register(target, Error{Code: CodeInvalid, Message: message, Field: field, Reason: reason})
}

func register(target error, as Error) {
	if as.Message == "" {
		as.Message = target.Error()
	}
	registryMu.Lock()
	defer registryMu.Unlock()
	for i := range registry {
		if sameError(registry[i].target, target) {
			registry[i].as = as
			return
		}
	}
	registry = append(registry, registration{target: target, as: as})
}

// From converts err for the frontend: an *Error in err's chain is returned
// as is; otherwise the outermost registered sentinel in the chain gives a
// new *Error with its code and message; anything else is internal with
// err's text. From(nil) is nil.
//
// "Outermost" is the shallowest match: the chain is walked breadth-first
// from err inwards, the errors one Unwrap() []error (a multi-%w Errorf,
// errors.Join) returns being visited left to right. At each error a
// sentinel matches as errors.Is matches it there: the error equals the
// sentinel or its Is method says so. The wrapping layer knows more about
// what the user was doing than the layer it wraps, so it decides — not the
// order in which the sentinels were registered.
func From(err error) *Error {
	if err == nil {
		return nil
	}
	var e *Error
	if errors.As(err, &e) && e != nil {
		return e
	}
	registryMu.RLock()
	defer registryMu.RUnlock()
	for level := []error{err}; len(level) > 0; {
		var next []error
		for _, cur := range level {
			if as, ok := lookup(cur); ok {
				return &as
			}
			switch u := cur.(type) {
			case interface{ Unwrap() error }:
				if inner := u.Unwrap(); inner != nil {
					next = append(next, inner)
				}
			case interface{ Unwrap() []error }:
				for _, inner := range u.Unwrap() {
					if inner != nil {
						next = append(next, inner)
					}
				}
			}
		}
		level = next
	}
	return New(CodeInternal, err.Error())
}

// lookup returns the registration of the sentinel cur itself is — equal to
// it, or claiming it through an Is method — without unwrapping cur. The
// caller holds registryMu.
func lookup(cur error) (Error, bool) {
	isser, hasIs := cur.(interface{ Is(error) bool })
	for _, r := range registry {
		if sameError(cur, r.target) || (hasIs && isser.Is(r.target)) {
			return r.as, true
		}
	}
	return Error{}, false
}

// sameError reports a == b without panicking on a dynamic type that is not
// comparable (as errors.Is guards it).
func sameError(a, b error) bool {
	ta := reflect.TypeOf(a)
	return ta == reflect.TypeOf(b) && ta.Comparable() && a == b
}

// Marshal returns the JSON of From(err). It is the application's
// MarshalError (signature func(error) []byte): it never fails, and for a nil
// error it returns nil so Wails falls back to its default.
func Marshal(err error) []byte {
	e := From(err)
	if e == nil {
		return nil
	}
	b, jerr := json.Marshal(e)
	if jerr != nil {
		// Unreachable: Error holds only strings.
		return nil
	}
	return b
}

// The engine's sentinels the frontend must tell apart. The engine packages
// do not import apperr, so they are registered here; library and session
// register their own.
func init() {
	Register(store.ErrNotFound, CodeNotFound, "")
	RegisterInvalid(picks.ErrDirHasOriginals, "picks_dir", "has_originals", "")
	RegisterInvalid(safefs.ErrSymlink, "", "symlink", "")
}
