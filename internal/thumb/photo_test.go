package thumb

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/volodymyrsmirnov/choix/internal/platform"
	"github.com/volodymyrsmirnov/choix/internal/platform/platformtest"
)

func TestPhoto_Full4096FollowsSourceKind(t *testing.T) {
	ctx := context.Background()

	t.Run("HEIC by long side", func(t *testing.T) {
		heic := filepath.Join(t.TempDir(), "IMG_0001.HEIC")
		if err := os.WriteFile(heic, []byte("not decoded by the fake"), 0o600); err != nil {
			t.Fatal(err)
		}
		s := &platformtest.Stills{Props: map[string]map[string]any{heic: {"PixelWidth": 100, "PixelHeight": 8000}}}
		img, err := Photo(ctx, s, heic, WidthFullJPEG)
		if err != nil {
			t.Fatalf("Photo: %v", err)
		}
		calls := s.Calls()
		if len(calls) != 1 || calls[0].Path != heic || calls[0].Limit != 4096 || calls[0].Fit != platform.FitLongSide {
			t.Fatalf("calls = %+v; want one Path call (4096, FitLongSide)", calls)
		}
		if img.Width != 51 || img.Height != 4096 {
			t.Fatalf("image = %dx%d; want 51x4096", img.Width, img.Height)
		}
	})

	t.Run("RAF by width on the embedded JPEG", func(t *testing.T) {
		raf := platformtest.MakeRAF(t, platformtest.MakeJPEG(t, 600, 400))
		s := &platformtest.Stills{}
		img, err := Photo(ctx, s, raf, WidthFullJPEG)
		if err != nil {
			t.Fatalf("Photo: %v", err)
		}
		calls := s.Calls()
		if len(calls) != 1 || calls[0].Path != "" || calls[0].Data == nil || calls[0].Limit != 4096 || calls[0].Fit != platform.FitWidth {
			t.Fatalf("got %d calls; want one Data call (4096, FitWidth)", len(calls))
		}
		if img.Width != 600 || img.Height != 400 {
			t.Fatalf("image = %dx%d; want 600x400 (never upscaled)", img.Width, img.Height)
		}
	})
}

func TestPhoto_NilStillsIsError(t *testing.T) {
	if _, err := Photo(context.Background(), nil, "/x/a.jpg", WidthThumb); err == nil {
		t.Fatal("Photo without Stills: want an error")
	}
}

func TestPhoto_CancelledReturnsContextError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	raf := platformtest.MakeRAF(t, platformtest.MakeJPEG(t, 64, 64))
	_, err := Photo(ctx, &platformtest.Stills{}, raf, WidthThumb)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Photo = %v; want context.Canceled", err)
	}
}
