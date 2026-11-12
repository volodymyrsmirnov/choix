//go:build darwin

package thumb

import (
	"testing"

	"github.com/volodymyrsmirnov/choix/internal/platform/darwin/avmedia"
	"github.com/volodymyrsmirnov/choix/internal/platform/darwin/imageio"
	"github.com/volodymyrsmirnov/choix/internal/platform/platformtest"
)

// The sizing contract holds on the real ImageIO, not only on the fake: a
// plain JPEG is bounded by its long side, a RAF's embedded JPEG by width.
func TestBuild_RealImageIOSizing(t *testing.T) {
	e := newBuildEnv(t)
	e.b.Stills = imageio.New()
	e.b.Video = avmedia.New()

	jpg := e.add("Day1/IMG_0001.jpg", "photo", platformtest.MakeJPEG(t, 500, 1000))
	raf := e.add("Day1/DSCF0002.RAF", "photo", fixture(t, platformtest.MakeRAF(t, platformtest.MakeJPEG(t, 500, 1000))))
	dng := e.add("Day1/IMG_0003.DNG", "photo", fixture(t, platformtest.MakeDNG(t, platformtest.DNGOptions{
		RawStrip: []byte("raw"),
		Previews: []platformtest.DNGPreview{{JPEG: platformtest.MakeJPEG(t, 500, 1000), Width: 500, Height: 1000}},
	})))
	notJPEG := e.add("Day1/fake.jpg", "photo", []byte("this is not a JPEG"))
	for _, id := range []int64{jpg, raf, dng, notJPEG} {
		_ = e.build(id)
	}

	e.wantStatus(jpg, "thumbed")
	e.wantSize(jpg, TierThumb, 128, 256)
	e.wantSize(jpg, TierPreview, 500, 1000)
	for _, id := range []int64{raf, dng} {
		e.wantStatus(id, "thumbed")
		e.wantSize(id, TierThumb, 256, 512)
		e.wantSize(id, TierPreview, 500, 1000)
	}
	e.wantFailed(notJPEG, "")
}
