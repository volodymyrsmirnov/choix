package apperr

import (
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/volodymyrsmirnov/choix/internal/picks"
	"github.com/volodymyrsmirnov/choix/internal/safefs"
	"github.com/volodymyrsmirnov/choix/internal/store"
)

func decode(t *testing.T, b []byte) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("Marshal produced invalid JSON %q: %v", b, err)
	}
	return m
}

func TestMarshal_AppError(t *testing.T) {
	err := Invalid("picks_dir", "absolute", "picks folder must be relative")
	got := decode(t, Marshal(fmt.Errorf("save settings: %w", err)))
	want := map[string]any{
		"code":    "invalid",
		"message": "picks folder must be relative",
		"field":   "picks_dir",
		"reason":  "absolute",
	}
	if len(got) != len(want) {
		t.Fatalf("keys = %v, want %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %v, want %v", k, got[k], v)
		}
	}
}

func TestMarshal_OmitsEmptyFieldAndReason(t *testing.T) {
	got := decode(t, Marshal(New(CodeUnavailable, "quitting")))
	if len(got) != 2 || got["code"] != "unavailable" || got["message"] != "quitting" {
		t.Errorf("Marshal = %v, want only code and message", got)
	}
}

func TestMarshal_PlainErrorIsInternal(t *testing.T) {
	got := decode(t, Marshal(errors.New("disk on fire")))
	if len(got) != 2 || got["code"] != "internal" || got["message"] != "disk on fire" {
		t.Errorf("Marshal = %v, want {code: internal, message: disk on fire}", got)
	}
}

func TestMarshal_NilFallsBack(t *testing.T) {
	if b := Marshal(nil); b != nil {
		t.Errorf("Marshal(nil) = %q, want nil (Wails falls back to its default)", b)
	}
	if e := From(nil); e != nil {
		t.Errorf("From(nil) = %v, want nil", e)
	}
}

func TestFrom_AppErrorPassesThrough(t *testing.T) {
	want := New(CodeNotOpen, "no folder is open")
	if got := From(fmt.Errorf("ctx: %w", want)); got != want {
		t.Errorf("From = %#v, want the same *Error %#v", got, want)
	}
}

func TestFrom_RegisteredSentinel(t *testing.T) {
	errA := errors.New("test: sentinel a")
	errB := errors.New("test: sentinel b")
	Register(errA, CodeOffline, "The folder is offline.")
	Register(errB, CodeNotFound, "")

	tests := []struct {
		name string
		err  error
		code string
		msg  string
	}{
		{"bare", errA, CodeOffline, "The folder is offline."},
		{"wrapped", fmt.Errorf("open folder 1: %w", errA), CodeOffline, "The folder is offline."},
		{"double wrapped", fmt.Errorf("a: %w", fmt.Errorf("b: %w", errA)), CodeOffline, "The folder is offline."},
		{"own text", fmt.Errorf("lookup: %w", errB), CodeNotFound, "test: sentinel b"},
		{"joined", errors.Join(errors.New("other"), errB), CodeNotFound, "test: sentinel b"},
		{"unregistered", errors.New("test: unknown"), CodeInternal, "test: unknown"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := From(tt.err)
			if got.Code != tt.code || got.Message != tt.msg || got.Field != "" || got.Reason != "" {
				t.Errorf("From = %+v, want code %q message %q", got, tt.code, tt.msg)
			}
		})
	}
}

// wrappingSentinel is a sentinel that itself wraps another error, so two
// registered sentinels can sit in one linear chain.
type wrappingSentinel struct {
	msg   string
	inner error
}

func (w *wrappingSentinel) Error() string { return w.msg + ": " + w.inner.Error() }
func (w *wrappingSentinel) Unwrap() error { return w.inner }

// isSentinel claims, through an Is method, to be target.
type isSentinel struct{ target error }

func (s isSentinel) Error() string        { return "is-sentinel" }
func (s isSentinel) Is(target error) bool { return target == s.target }

func TestFrom_OutermostRegisteredSentinelWins(t *testing.T) {
	inner := errors.New("test: inner sentinel")
	deep := errors.New("test: deep sentinel")
	// Registered inner first: registration order must not decide.
	Register(inner, CodeNotFound, "inner")
	Register(deep, CodeReadOnly, "deep")
	outer := &wrappingSentinel{msg: "test: outer sentinel", inner: inner}
	Register(outer, CodeOffline, "outer")
	viaIs := isSentinel{target: deep}

	tests := []struct {
		name string
		err  error
		want string // Message of the winning registration
	}{
		{"outer wraps inner", fmt.Errorf("op: %w", outer), "outer"},
		{"inner alone", fmt.Errorf("op: %w", inner), "inner"},
		{"multi %w: left first at one depth", fmt.Errorf("%w: %w", deep, inner), "deep"},
		{"multi %w: right first at one depth", fmt.Errorf("%w: %w", inner, deep), "inner"},
		{"multi %w: shallower right beats deeper left",
			fmt.Errorf("%w: %w", fmt.Errorf("ctx: %w", deep), inner), "inner"},
		{"join: shallower beats deeper",
			errors.Join(fmt.Errorf("a: %w", fmt.Errorf("b: %w", inner)), fmt.Errorf("c: %w", deep)), "deep"},
		{"join nested in a wrap", fmt.Errorf("op: %w", errors.Join(errors.New("x"), deep, inner)), "deep"},
		{"Is method counts at its depth", fmt.Errorf("op: %w: %w", viaIs, fmt.Errorf("ctx: %w", outer)), "deep"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := From(tt.err).Message; got != tt.want {
				t.Errorf("From(%v).Message = %q, want %q", tt.err, got, tt.want)
			}
		})
	}
}

// sliceError is an error type whose values are not comparable.
type sliceError []string

func (s sliceError) Error() string { return "slice error" }

func TestFrom_UncomparableErrorsDoNotPanic(t *testing.T) {
	Register(sliceError{"registered"}, CodeOffline, "")
	Register(sliceError{"again"}, CodeOffline, "") // compared with the first: must not panic
	got := From(fmt.Errorf("op: %w", sliceError{"other"}))
	if got.Code != CodeInternal {
		t.Errorf("From = %+v, want internal (an uncomparable sentinel never matches by equality)", got)
	}
}

func TestFrom_ReturnsACopyOfTheRegistration(t *testing.T) {
	errC := errors.New("test: sentinel c")
	Register(errC, CodeReadOnly, "read-only")
	From(errC).Message = "mutated"
	if got := From(errC).Message; got != "read-only" {
		t.Errorf("Message after mutating an earlier result = %q, want %q", got, "read-only")
	}
}

func TestRegister_ReplacesEarlierRegistration(t *testing.T) {
	errD := errors.New("test: sentinel d")
	Register(errD, CodeOffline, "")
	Register(errD, CodeAccessLost, "lost")
	if got := From(errD); got.Code != CodeAccessLost || got.Message != "lost" {
		t.Errorf("From = %+v, want the later registration", got)
	}
}

func TestRegisterInvalid(t *testing.T) {
	errE := errors.New("test: sentinel e")
	RegisterInvalid(errE, "picks_dir", "has_originals", "")
	got := From(fmt.Errorf("export: %w", errE))
	want := Error{Code: CodeInvalid, Message: "test: sentinel e", Field: "picks_dir", Reason: "has_originals"}
	if *got != want {
		t.Errorf("From = %+v, want %+v", *got, want)
	}
}

func TestEngineSentinelsRegistered(t *testing.T) {
	tests := []struct {
		name   string
		err    error
		want   Error
		ownMsg error
	}{
		{"store not found", fmt.Errorf("get file 3: %w", store.ErrNotFound),
			Error{Code: CodeNotFound}, store.ErrNotFound},
		{"picks dir has originals", fmt.Errorf("%w (2 under x)", picks.ErrDirHasOriginals),
			Error{Code: CodeInvalid, Field: "picks_dir", Reason: "has_originals"}, picks.ErrDirHasOriginals},
		{"symlink", fmt.Errorf("open picks: %w", safefs.ErrSymlink),
			Error{Code: CodeInvalid, Reason: "symlink"}, safefs.ErrSymlink},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.want.Message = tt.ownMsg.Error()
			if got := From(tt.err); *got != tt.want {
				t.Errorf("From = %+v, want %+v", *got, tt.want)
			}
		})
	}
}

func TestError_ErrorText(t *testing.T) {
	if got := New(CodeInternal, "boom").Error(); got != "boom" {
		t.Errorf("Error() = %q, want %q", got, "boom")
	}
	if got := New(CodeNotOpen, "").Error(); got != CodeNotOpen {
		t.Errorf("Error() of an empty message = %q, want the code", got)
	}
}
