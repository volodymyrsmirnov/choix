package apperr_test

import (
	"fmt"
	"testing"

	"github.com/volodymyrsmirnov/choix/internal/app/apperr"
	"github.com/volodymyrsmirnov/choix/internal/app/library"
	"github.com/volodymyrsmirnov/choix/internal/app/session"
	"github.com/volodymyrsmirnov/choix/internal/safefs"
)

// TestFrom_UnsafeStateWrappingSymlink: a session open refused because .choix
// is a symbolic link wraps both session.ErrUnsafeState and safefs.ErrSymlink
// (session.go: "%w: %w"). The outer one — whichever it is — decides what the
// frontend receives, whatever order the sentinels were registered in.
func TestFrom_UnsafeStateWrappingSymlink(t *testing.T) {
	symlink := fmt.Errorf("open .choix: %w", safefs.ErrSymlink)
	unsafe := fmt.Errorf("check state.db: %w", session.ErrUnsafeState)
	unsafeState := apperr.Error{Code: apperr.CodeInvalid, Message: session.UnsafeStateMessage, Reason: "unsafe_state"}
	symlinkErr := apperr.Error{Code: apperr.CodeInvalid, Message: safefs.ErrSymlink.Error(), Reason: "symlink"}
	tests := []struct {
		name string
		err  error
		want apperr.Error
	}{
		{"session wraps safefs (session.go's shape)",
			fmt.Errorf("session: open f: %w: %w", session.ErrUnsafeState, symlink), unsafeState},
		{"session wraps safefs, wrapped again",
			fmt.Errorf("acquire: %w", fmt.Errorf("session: open f: %w: %w", session.ErrUnsafeState, symlink)), unsafeState},
		{"session wraps safefs, symlink listed first but deeper",
			fmt.Errorf("%w: %w", symlink, session.ErrUnsafeState), unsafeState},
		{"safefs wraps session",
			fmt.Errorf("publish: %w: %w", safefs.ErrSymlink, unsafe), symlinkErr},
		{"safefs wraps session, unsafe listed first but deeper",
			fmt.Errorf("%w: %w", unsafe, safefs.ErrSymlink), symlinkErr},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := apperr.From(tt.err); *got != tt.want {
				t.Errorf("From = %+v, want %+v", *got, tt.want)
			}
		})
	}
}

// TestAppSentinelsRegistered pins what the frontend receives for the
// sentinel errors library and session register in their init.
func TestAppSentinelsRegistered(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want apperr.Error
	}{
		{"library offline", library.ErrOffline,
			apperr.Error{Code: apperr.CodeOffline, Message: library.ErrOffline.Error()}},
		{"library access lost", library.ErrAccessLost,
			apperr.Error{Code: apperr.CodeAccessLost, Message: library.AccessLostMessage}},
		{"library not found", library.ErrNotFound,
			apperr.Error{Code: apperr.CodeNotFound, Message: library.ErrNotFound.Error()}},
		{"library not a directory", library.ErrNotDirectory,
			apperr.Error{Code: apperr.CodeInvalid, Message: library.ErrNotDirectory.Error()}},
		{"session read-only", session.ErrReadOnly,
			apperr.Error{Code: apperr.CodeReadOnly, Message: session.ReadOnlyMessage}},
		{"session unsafe state", session.ErrUnsafeState,
			apperr.Error{Code: apperr.CodeInvalid, Message: session.UnsafeStateMessage, Reason: "unsafe_state"}},
		{"session shutting down", session.ErrShuttingDown,
			apperr.Error{Code: apperr.CodeUnavailable, Message: session.ErrShuttingDown.Error()}},
		// A stale session still closing is not quitting: the frontend must
		// not drop it silently as it drops unavailable.
		{"session busy", session.ErrBusy,
			apperr.Error{Code: apperr.CodeBusy, Message: session.ErrBusy.Error()}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := apperr.From(fmt.Errorf("op on folder 0123456789abcdef: %w", tt.err)); *got != tt.want {
				t.Errorf("From = %+v, want %+v", *got, tt.want)
			}
		})
	}
}
