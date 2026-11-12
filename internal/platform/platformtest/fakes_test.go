package platformtest_test

import (
	"bytes"
	"context"
	"errors"
	"image/jpeg"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"
	"unsafe"

	"github.com/volodymyrsmirnov/choix/internal/platform"
	"github.com/volodymyrsmirnov/choix/internal/platform/platformtest"
)

// The fakes satisfy the interfaces they stand in for.
var (
	_ platform.Bookmarks    = (*platformtest.Bookmarks)(nil)
	_ platform.Volumes      = (*platformtest.Volumes)(nil)
	_ platform.Workspace    = (*platformtest.Workspace)(nil)
	_ platform.Stills       = (*platformtest.Stills)(nil)
	_ platform.Video        = (*platformtest.Video)(nil)
	_ platform.TitleBar     = (*platformtest.TitleBar)(nil)
	_ platform.WindowChrome = (*platformtest.WindowChrome)(nil)
)

// TestFakeTitleBar_CenterY: the fake answers its fields and refuses a nil
// window, like the real one.
func TestFakeTitleBar_CenterY(t *testing.T) {
	var w byte
	tb := &platformtest.TitleBar{CenterY: 26}
	if y, err := tb.ButtonsCenterY(unsafe.Pointer(&w)); err != nil || y != 26 {
		t.Errorf("ButtonsCenterY = %v, %v; want 26, nil", y, err)
	}
	if _, err := tb.ButtonsCenterY(nil); err == nil {
		t.Error("ButtonsCenterY(nil) = nil error; want an error")
	}
	tb.CenterYErr = errors.New("no buttons")
	if y, err := tb.ButtonsCenterY(unsafe.Pointer(&w)); err == nil || y != 0 {
		t.Errorf("ButtonsCenterY with CenterYErr = %v, %v; want 0 and the error", y, err)
	}
}

// TestFakeWindowChrome_Records: every call is recorded with its window and
// arguments; a nil window is refused and not recorded; the error fields are
// returned.
func TestFakeWindowChrome_Records(t *testing.T) {
	var w byte
	p := unsafe.Pointer(&w)
	c := &platformtest.WindowChrome{Changed: true, Focused: true}
	if changed, err := c.KeepMinSize(p, 960, 600); err != nil || !changed {
		t.Errorf("KeepMinSize = %v, %v; want true, nil", changed, err)
	}
	if focused, err := c.FocusPage(p); err != nil || !focused {
		t.Errorf("FocusPage = %v, %v; want true, nil", focused, err)
	}
	if err := c.SetPageBackground(p, 18, 20, 21); err != nil {
		t.Errorf("SetPageBackground = %v; want nil", err)
	}
	if _, err := c.KeepMinSize(nil, 960, 600); err == nil {
		t.Error("KeepMinSize(nil) = nil error")
	}
	if _, err := c.FocusPage(nil); err == nil {
		t.Error("FocusPage(nil) = nil error")
	}
	if err := c.SetPageBackground(nil, 1, 2, 3); err == nil {
		t.Error("SetPageBackground(nil) = nil error")
	}
	if got := c.MinSizes(); len(got) != 1 || got[0] != (platformtest.MinSizeCall{Window: p, Width: 960, Height: 600}) {
		t.Errorf("MinSizes = %v", got)
	}
	if got := c.Focuses(); len(got) != 1 || got[0] != p {
		t.Errorf("Focuses = %v", got)
	}
	if got := c.Backgrounds(); len(got) != 1 || got[0] != (platformtest.BackgroundCall{Window: p, Red: 18, Green: 20, Blue: 21}) {
		t.Errorf("Backgrounds = %v", got)
	}
	boom := errors.New("boom")
	c = &platformtest.WindowChrome{Changed: true, Focused: true, KeepErr: boom, FocusErr: boom, BackgroundErr: boom}
	if changed, err := c.KeepMinSize(p, 1, 1); !errors.Is(err, boom) || changed {
		t.Errorf("KeepMinSize with KeepErr = %v, %v", changed, err)
	}
	if focused, err := c.FocusPage(p); !errors.Is(err, boom) || focused {
		t.Errorf("FocusPage with FocusErr = %v, %v", focused, err)
	}
	if err := c.SetPageBackground(p, 1, 2, 3); !errors.Is(err, boom) {
		t.Errorf("SetPageBackground with BackgroundErr = %v", err)
	}
}

func TestFakeBookmarks_ResolveFollowsMove(t *testing.T) {
	b := &platformtest.Bookmarks{}
	data, err := b.Create("/a")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	b.Move("/a", "/b")
	path, stale, err := b.Resolve(data)
	if err != nil || path != "/b" || stale {
		t.Fatalf("Resolve = %q, %v, %v; want \"/b\", false, nil", path, stale, err)
	}
}

func TestFakeBookmarks_DeleteIsErrGone(t *testing.T) {
	b := &platformtest.Bookmarks{}
	data, err := b.Create("/a")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	b.Delete("/a")
	if _, _, err := b.Resolve(data); !errors.Is(err, platform.ErrGone) {
		t.Fatalf("Resolve err = %v; want ErrGone", err)
	}
	if _, _, err := b.StartAccess(data); !errors.Is(err, platform.ErrGone) {
		t.Fatalf("StartAccess err = %v; want ErrGone", err)
	}
}

func TestFakeBookmarks_UnusableIsErrUnusable(t *testing.T) {
	b := &platformtest.Bookmarks{}
	data, err := b.Create("/a")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	b.MakeUnusable("/a")
	if _, _, err := b.Resolve(data); !errors.Is(err, platform.ErrUnusable) {
		t.Fatalf("Resolve err = %v; want ErrUnusable", err)
	}
	if _, _, err := b.StartAccess(data); !errors.Is(err, platform.ErrUnusable) {
		t.Fatalf("StartAccess err = %v; want ErrUnusable", err)
	}
	if _, _, err := b.Resolve([]byte("not a bookmark")); !errors.Is(err, platform.ErrUnusable) {
		t.Fatalf("Resolve(garbage) err = %v; want ErrUnusable", err)
	}

	// Adding the folder again yields a fresh, usable bookmark.
	fresh, err := b.Create("/a")
	if err != nil {
		t.Fatalf("Create again: %v", err)
	}
	if path, _, err := b.Resolve(fresh); err != nil || path != "/a" {
		t.Fatalf("Resolve(fresh) = %q, %v; want \"/a\", nil", path, err)
	}

	// A denied access still resolves but cannot start.
	b.DenyAccess("/a")
	if _, _, err := b.Resolve(fresh); err != nil {
		t.Fatalf("Resolve after DenyAccess: %v", err)
	}
	if _, _, err := b.StartAccess(fresh); !errors.Is(err, platform.ErrUnusable) {
		t.Fatalf("StartAccess after DenyAccess err = %v; want ErrUnusable", err)
	}
}

func TestFakeBookmarks_StartStopTracksActive(t *testing.T) {
	b := &platformtest.Bookmarks{}
	data, err := b.Create("/a")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	path, stop, err := b.StartAccess(data)
	if err != nil || path != "/a" {
		t.Fatalf("StartAccess = %q, %v; want \"/a\", nil", path, err)
	}
	if got := b.Active(); !slices.Equal(got, []string{"/a"}) {
		t.Fatalf("Active() = %q; want [/a]", got)
	}
	stop()
	stop()
	if got := b.Active(); len(got) != 0 {
		t.Fatalf("Active() after stop = %q; want []", got)
	}
}

func TestFakeBookmarks_RefreshReturnsNewBytes(t *testing.T) {
	b := &platformtest.Bookmarks{}
	data, err := b.Create("/a")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	b.MarkStale("/a")
	if _, stale, err := b.Resolve(data); err != nil || !stale {
		t.Fatalf("Resolve after MarkStale: stale = %v, err = %v; want true, nil", stale, err)
	}

	fresh, err := b.Refresh(data)
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if bytes.Equal(fresh, data) {
		t.Fatal("Refresh returned the same bytes")
	}
	path, stale, err := b.Resolve(fresh)
	if err != nil || path != "/a" || stale {
		t.Fatalf("Resolve(refreshed) = %q, %v, %v; want \"/a\", false, nil", path, stale, err)
	}
	if n := b.Refreshed(); n != 1 {
		t.Fatalf("Refreshed() = %d; want 1", n)
	}
	if got := b.Active(); len(got) != 0 {
		t.Fatalf("Active() after Refresh = %q; want []", got)
	}

	// A failed refresh is not counted.
	b.Delete("/a")
	if _, err := b.Refresh(fresh); !errors.Is(err, platform.ErrGone) {
		t.Fatalf("Refresh after Delete err = %v; want ErrGone", err)
	}
	if n := b.Refreshed(); n != 1 {
		t.Fatalf("Refreshed() after failure = %d; want 1", n)
	}
}

func TestFakeVolumes_UnmountMount(t *testing.T) {
	v := &platformtest.Volumes{}
	card := platform.VolumeInfo{UUID: "UUID-CARD", Name: "CARD", Removable: true}
	v.Add("/Volumes/CARD", card)

	got, err := v.Info("/Volumes/CARD/DCIM/100TEST")
	if err != nil || got != card {
		t.Fatalf("Info = %+v, %v; want %+v, nil", got, err, card)
	}
	if !v.Mounted("UUID-CARD") {
		t.Fatal("Mounted after Add = false")
	}
	v.Unmount("UUID-CARD")
	if v.Mounted("UUID-CARD") {
		t.Fatal("Mounted after Unmount = true")
	}
	if _, err := v.Info("/Volumes/CARD/DCIM"); err == nil {
		t.Fatal("Info on an unmounted volume with no volume below it: want an error")
	}
	v.Mount("UUID-CARD")
	if !v.Mounted("UUID-CARD") {
		t.Fatal("Mounted after Mount = false")
	}
	if v.Mounted("UUID-UNKNOWN") {
		t.Fatal("Mounted(unknown) = true")
	}

	v.SetAbsent("/Volumes/CARD/Trip", "UUID-CARD", true)
	if !v.AbsentOn("/Volumes/CARD/Trip", "UUID-CARD") {
		t.Fatal("AbsentOn after SetAbsent(true) = false")
	}
	if v.AbsentOn("/Volumes/CARD/Trip", "UUID-OTHER") {
		t.Fatal("AbsentOn with another UUID = true")
	}
}

func TestFakeVolumes_TrashUnknownIsError(t *testing.T) {
	v := &platformtest.Volumes{}
	v.TrashUnknown("/Users/test/Unknown")
	if _, err := v.InTrash("/Users/test/Unknown"); err == nil {
		t.Fatal("InTrash on an unknown path: want an error")
	}

	v.Trash("/Users/test/.Trash/Trip")
	if in, err := v.InTrash("/Users/test/.Trash/Trip"); err != nil || !in {
		t.Fatalf("InTrash(trashed) = %v, %v; want true, nil", in, err)
	}
	if in, err := v.InTrash("/Users/test/Pictures"); err != nil || in {
		t.Fatalf("InTrash(other) = %v, %v; want false, nil", in, err)
	}

	b := &platformtest.Bookmarks{}
	data, err := b.Create("/Volumes/CARD/Trip")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	v.TrashNeedsAccess("/Volumes/CARD/Trip", b)
	if _, err := v.InTrash("/Volumes/CARD/Trip"); err == nil {
		t.Fatal("InTrash without access: want an error")
	}
	_, stop, err := b.StartAccess(data)
	if err != nil {
		t.Fatalf("StartAccess: %v", err)
	}
	defer stop()
	if in, err := v.InTrash("/Volumes/CARD/Trip"); err != nil || in {
		t.Fatalf("InTrash under access = %v, %v; want false, nil", in, err)
	}
}

// The tests below cover the media and workspace fakes' own contract.

func TestFakeStills_ThumbnailSizes(t *testing.T) {
	s := &platformtest.Stills{}
	src := platformtest.MakeJPEG(t, 400, 300)
	ctx := context.Background()

	cases := []struct {
		name          string
		in            platform.ImageInput
		limit         int
		fit           platform.Fit
		wantW, wantH  int
		wantErr       bool
		wantDecodable bool
	}{
		{name: "width", in: platform.ImageInput{Data: src}, limit: 200, fit: platform.FitWidth, wantW: 200, wantH: 150},
		{name: "long side", in: platform.ImageInput{Data: src}, limit: 100, fit: platform.FitLongSide, wantW: 100, wantH: 75},
		{name: "never upscaled", in: platform.ImageInput{Data: src}, limit: 1600, fit: platform.FitLongSide, wantW: 400, wantH: 300},
		{name: "orientation 6 swaps", in: platform.ImageInput{Data: src, Orientation: 6}, limit: 150, fit: platform.FitWidth, wantW: 150, wantH: 200},
		{name: "path without props", in: platform.ImageInput{Path: "/x/a.heic"}, limit: 256, fit: platform.FitLongSide, wantW: 256, wantH: 256},
		{name: "path and data", in: platform.ImageInput{Path: "/x/a.heic", Data: src}, limit: 256, fit: platform.FitWidth, wantErr: true},
		{name: "neither", in: platform.ImageInput{}, limit: 256, fit: platform.FitWidth, wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			img, err := s.Thumbnail(ctx, tc.in, tc.limit, tc.fit)
			if tc.wantErr {
				if err == nil {
					t.Fatal("want an error")
				}
				return
			}
			if err != nil {
				t.Fatalf("Thumbnail: %v", err)
			}
			if img.Width != tc.wantW || img.Height != tc.wantH {
				t.Fatalf("size = %dx%d; want %dx%d", img.Width, img.Height, tc.wantW, tc.wantH)
			}
			dec, err := jpeg.Decode(bytes.NewReader(img.JPEG))
			if err != nil {
				t.Fatalf("decode result: %v", err)
			}
			if b := dec.Bounds(); b.Dx() != tc.wantW || b.Dy() != tc.wantH {
				t.Fatalf("decoded size = %dx%d; want %dx%d", b.Dx(), b.Dy(), tc.wantW, tc.wantH)
			}
		})
	}
}

func TestFakeStills_DamagedDataFails(t *testing.T) {
	s := &platformtest.Stills{}
	src := platformtest.MakeJPEG(t, 64, 64)
	if _, err := s.Thumbnail(context.Background(), platform.ImageInput{Data: src[:len(src)/2]}, 32, platform.FitWidth); err == nil {
		t.Fatal("Thumbnail of a truncated JPEG: want an error")
	}
}

func TestFakeStills_PropsFailAndGate(t *testing.T) {
	wantErr := errors.New("unreadable")
	s := &platformtest.Stills{
		Props: map[string]map[string]any{"/x/a.jpg": {"PixelWidth": 120, "PixelHeight": 80}},
		Fail:  map[string]error{"/x/bad.jpg": wantErr},
		Gate:  make(chan struct{}),
	}
	ctx := context.Background()

	done := make(chan error, 1)
	go func() {
		props, err := s.Properties(ctx, platform.ImageInput{Path: "/x/a.jpg"})
		if err == nil && props["PixelWidth"] != 120 {
			err = errors.New("wrong properties")
		}
		done <- err
	}()
	select {
	case err := <-done:
		t.Fatalf("Properties returned %v before the gate opened", err)
	case <-time.After(20 * time.Millisecond):
	}
	close(s.Gate)
	if err := <-done; err != nil {
		t.Fatalf("Properties: %v", err)
	}

	if _, err := s.Thumbnail(ctx, platform.ImageInput{Path: "/x/bad.jpg"}, 64, platform.FitWidth); !errors.Is(err, wantErr) {
		t.Fatalf("Thumbnail err = %v; want %v", err, wantErr)
	}
	img, err := s.Thumbnail(ctx, platform.ImageInput{Path: "/x/a.jpg"}, 60, platform.FitWidth)
	if err != nil || img.Width != 60 || img.Height != 40 {
		t.Fatalf("Thumbnail from props = %dx%d, %v; want 60x40, nil", img.Width, img.Height, err)
	}

	// A held call returns ctx.Err() when its context is cancelled.
	held := &platformtest.Stills{Gate: make(chan struct{})}
	defer close(held.Gate)
	cctx, cancel := context.WithTimeout(ctx, 20*time.Millisecond)
	defer cancel()
	if _, err := held.Properties(cctx, platform.ImageInput{Path: "/x/a.jpg"}); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("held Properties err = %v; want context.DeadlineExceeded", err)
	}
}

func TestFakeStills_ReadPathsSizesFromTheFile(t *testing.T) {
	dir := t.TempDir()
	jpg := filepath.Join(dir, "a.jpg")
	if err := os.WriteFile(jpg, platformtest.MakeJPEG(t, 500, 1000), 0o600); err != nil {
		t.Fatal(err)
	}
	empty := filepath.Join(dir, "empty.jpg")
	if err := os.WriteFile(empty, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	notJPEG := filepath.Join(dir, "b.heic")
	if err := os.WriteFile(notJPEG, []byte("not a jpeg"), 0o600); err != nil {
		t.Fatal(err)
	}
	s := &platformtest.Stills{
		ReadPaths: true,
		Props:     map[string]map[string]any{notJPEG: {"PixelWidth": 300, "PixelHeight": 200}},
	}
	ctx := context.Background()

	img, err := s.Thumbnail(ctx, platform.ImageInput{Path: jpg}, 256, platform.FitLongSide)
	if err != nil || img.Width != 128 || img.Height != 256 {
		t.Fatalf("Thumbnail of a 500x1000 file = %dx%d, %v; want 128x256, nil", img.Width, img.Height, err)
	}
	// A size in Props still wins over the file.
	img, err = s.Thumbnail(ctx, platform.ImageInput{Path: notJPEG}, 150, platform.FitWidth)
	if err != nil || img.Width != 150 || img.Height != 100 {
		t.Fatalf("Thumbnail with props = %dx%d, %v; want 150x100, nil", img.Width, img.Height, err)
	}
	for _, p := range []string{empty, filepath.Join(dir, "missing.jpg")} {
		if _, err := s.Thumbnail(ctx, platform.ImageInput{Path: p}, 64, platform.FitWidth); err == nil {
			t.Fatalf("Thumbnail of %s: want an error", filepath.Base(p))
		}
	}
}

func TestFakeStills_Calls(t *testing.T) {
	s := &platformtest.Stills{}
	ctx := context.Background()
	src := platformtest.MakeJPEG(t, 32, 16)

	if _, err := s.Properties(ctx, platform.ImageInput{Path: "/x/a.jpg"}); err != nil {
		t.Fatalf("Properties: %v", err)
	}
	if _, err := s.Thumbnail(ctx, platform.ImageInput{Data: src, Orientation: 6}, 16, platform.FitLongSide); err != nil {
		t.Fatalf("Thumbnail: %v", err)
	}
	// An invalid input is refused before it is recorded.
	if _, err := s.Properties(ctx, platform.ImageInput{}); err == nil {
		t.Fatal("Properties without input: want an error")
	}
	src[0] = 0 // the log holds a copy of Data

	got := s.Calls()
	if len(got) != 2 {
		t.Fatalf("Calls = %d; want 2", len(got))
	}
	if c := got[0]; c.Method != "Properties" || c.Path != "/x/a.jpg" || c.Data != nil || c.Limit != 0 {
		t.Fatalf("call 0 = %+v; want Properties of /x/a.jpg", c)
	}
	c := got[1]
	if c.Method != "Thumbnail" || c.Path != "" || c.Orientation != 6 || c.Limit != 16 || c.Fit != platform.FitLongSide {
		t.Fatalf("call 1 = %+v; want Thumbnail of data, orientation 6, 16, FitLongSide", c)
	}
	if len(c.Data) == 0 || c.Data[0] != 0xFF {
		t.Fatal("call 1 Data is not a copy of the input")
	}
}

func TestFakeVideo_Frames(t *testing.T) {
	v := &platformtest.Video{
		Props: map[string]map[string]any{"/x/clip.mov": {"PixelWidth": 64, "PixelHeight": 36}},
		Fail:  map[string]error{"/x/audio.mov": platform.ErrNoVideoTrack},
	}
	ctx := context.Background()
	frames, err := v.Frames(ctx, "/x/clip.mov", []float64{0, 0.5, 1})
	if err != nil || len(frames) != 3 {
		t.Fatalf("Frames = %d frames, %v; want 3, nil", len(frames), err)
	}
	for i, f := range frames {
		if f.Width != 64 || f.Height != 36 {
			t.Fatalf("frame %d = %dx%d; want 64x36", i, f.Width, f.Height)
		}
		if _, err := jpeg.Decode(bytes.NewReader(f.JPEG)); err != nil {
			t.Fatalf("frame %d: decode: %v", i, err)
		}
	}
	if _, err := v.Frames(ctx, "/x/audio.mov", []float64{0.5}); !errors.Is(err, platform.ErrNoVideoTrack) {
		t.Fatalf("Frames err = %v; want ErrNoVideoTrack", err)
	}
	if props, err := v.Properties(ctx, "/x/clip.mov"); err != nil || props["PixelWidth"] != 64 {
		t.Fatalf("Properties = %v, %v", props, err)
	}
}

func TestFakeWorkspace_RecordsCalls(t *testing.T) {
	w := &platformtest.Workspace{}
	if err := w.Reveal("/a/b.jpg"); err != nil {
		t.Fatalf("Reveal: %v", err)
	}
	if err := w.OpenFolder("/a"); err != nil {
		t.Fatalf("OpenFolder: %v", err)
	}
	if err := w.OpenURL("https://example.com/"); err != nil {
		t.Fatalf("OpenURL: %v", err)
	}
	if err := w.OpenURL("file:///etc/passwd"); err == nil {
		t.Fatal("OpenURL(file://): want an error")
	}
	want := []platformtest.WorkspaceCall{
		{Method: "Reveal", Arg: "/a/b.jpg"},
		{Method: "OpenFolder", Arg: "/a"},
		{Method: "OpenURL", Arg: "https://example.com/"},
	}
	if got := w.Calls(); !slices.Equal(got, want) {
		t.Fatalf("Calls() = %+v; want %+v", got, want)
	}
}
