package thumb

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/volodymyrsmirnov/choix/internal/platform"
	"github.com/volodymyrsmirnov/choix/internal/platform/platformtest"
)

// distinctFrames is a platform.Video whose frames all differ: frame i is a
// (40+i)×30 JPEG. It records what it returned.
type distinctFrames struct {
	platformtest.Video
	t      testing.TB
	frames []platform.Image
}

func (d *distinctFrames) Frames(ctx context.Context, path string, fractions []float64) ([]platform.Image, error) {
	if !slices.Equal(fractions, KeyframeFractions) {
		return nil, fmt.Errorf("fractions %v; want KeyframeFractions", fractions)
	}
	d.frames = nil
	for i := range fractions {
		w := 40 + i
		d.frames = append(d.frames, platform.Image{JPEG: platformtest.MakeJPEG(d.t, w, 30), Width: w, Height: 30})
	}
	return d.frames, nil
}

func TestBuild_VideoWritesKeyframesAndTiers(t *testing.T) {
	e := newBuildEnv(t)
	video := &distinctFrames{t: t}
	e.b.Video = video
	id := e.add("Day1/CLIP.MOV", "video", []byte("not decoded by the fake"))
	e.mustBuild(id)

	e.wantStatus(id, "thumbed")
	entries, err := os.ReadDir(KeyframesDir(e.root, id))
	if err != nil {
		t.Fatalf("read keyframes: %v", err)
	}
	var names []string
	for _, ent := range entries {
		names = append(names, ent.Name())
	}
	if want := []string{"1.jpg", "2.jpg", "3.jpg", "4.jpg", "5.jpg"}; !slices.Equal(names, want) {
		t.Fatalf("keyframes = %v; want %v", names, want)
	}
	// Each file holds its own full-size frame, in order.
	for i, f := range video.frames {
		got, err := os.ReadFile(filepath.Join(KeyframesDir(e.root, id), fmt.Sprintf("%d.jpg", i+1)))
		if err != nil {
			t.Fatalf("read keyframe %d: %v", i+1, err)
		}
		if !bytes.Equal(got, f.JPEG) {
			t.Fatalf("keyframe %d.jpg is not frame %d (%dx%d)", i+1, i+1, f.Width, f.Height)
		}
	}
	// Both tiers come from frame 1 (40x30, under both limits).
	e.wantSize(id, TierThumb, 40, 30)
	e.wantSize(id, TierPreview, 40, 30)
	for _, tier := range []string{TierThumb, TierPreview} {
		th, err := e.st.Thumbs().Get(id, tier)
		if err != nil {
			t.Fatalf("Thumbs.Get(%s): %v", tier, err)
		}
		if want := fmt.Sprintf(".choix/thumbs/%02x/%d-%s.jpg", id%256, id, tier); th.RelPath != want {
			t.Fatalf("%s rel_path = %q; want %q", tier, th.RelPath, want)
		}
		e.thumbSize(id, tier)
	}
}

func TestBuild_VideoTiersScaleByWidth(t *testing.T) {
	e := newBuildEnv(t)
	id := e.add("CLIP.MOV", "video", []byte("x"))
	e.video.Props = map[string]map[string]any{e.abs("CLIP.MOV"): {"PixelWidth": 500, "PixelHeight": 1000}}
	e.mustBuild(id)

	e.wantCalls(dataCall(256, platform.FitWidth), dataCall(1600, platform.FitWidth))
	e.wantSize(id, TierThumb, 256, 512)
	e.wantSize(id, TierPreview, 500, 1000)
}

func TestBuild_VideoWithoutTrackIsFailed(t *testing.T) {
	e := newBuildEnv(t)
	id := e.add("audio.mov", "video", []byte("x"))
	e.video.Fail = map[string]error{e.abs("audio.mov"): platform.ErrNoVideoTrack}
	if err := e.build(id); err == nil {
		t.Fatal("Build of a video without a track: want an error")
	}
	e.wantFailed(id, "no video track")
}

func TestKeyframeFractions(t *testing.T) {
	if want := []float64{0, 0.25, 0.5, 0.75, 0.95}; !slices.Equal(KeyframeFractions, want) {
		t.Fatalf("KeyframeFractions = %v; want %v", KeyframeFractions, want)
	}
}
