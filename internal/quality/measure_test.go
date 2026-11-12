package quality

import (
	"image"
	"math"
	"testing"
)

// refA is the generator a(x, y; k) of the spec's §6.3.
func refA(x, y, k int) int {
	fx, fy, fk := float64(x), float64(y), float64(k)
	v := 0.5 + 0.25*math.Sin(2*math.Pi*fx/(16*fk))*math.Cos(2*math.Pi*fy/(24*fk)) +
		0.1*math.Sin(2*math.Pi*(fx+2*fy)/(7*fk))
	v = math.Min(math.Max(v, 0), 1)
	return int(math.Floor(255*v + 0.5))
}

func clamp255(v int) uint8 {
	return uint8(min(max(v, 0), 255))
}

// refImage builds an NRGBA image of w×h whose pixel (x, y) is px(x, y).
func refImage(w, h int, px func(x, y int) (r, g, b uint8)) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			r, g, b := px(x, y)
			i := img.PixOffset(x, y)
			img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] = r, g, b, 255
		}
	}
	return img
}

func refImageA() *image.NRGBA {
	return refImage(640, 480, func(x, y int) (uint8, uint8, uint8) {
		v := uint8(refA(x, y, 1))
		return v, v, v
	})
}

func refNoise(x, y, mod int64) int64 {
	return ((x * 73856093) ^ (y * 19349663)) % mod
}

func channelSum(img *image.NRGBA) int64 {
	var s int64
	for i := 0; i < len(img.Pix); i += 4 {
		s += int64(img.Pix[i]) + int64(img.Pix[i+1]) + int64(img.Pix[i+2])
	}
	return s
}

func TestMeasure_ReferenceImages(t *testing.T) {
	type want struct {
		var1, var2, noise, dn1, dn2, midgrey, clip, raw, display float64
		sum                                                      int64 // 0: not given
	}
	cases := []struct {
		name string
		img  *image.NRGBA
		want want
	}{
		{"A", refImageA(), want{
			0.00150935137, 0.000315702244, 0.0339156863, 0.00138056273, 0.000313084169,
			0.123571826, 0, -40.7055461, 9.647, 117_498_873,
		}},
		{"B", refImage(640, 480, func(x, y int) (uint8, uint8, uint8) {
			v := clamp255(refA(x, y, 1) + int(refNoise(int64(x), int64(y), 41)) - 20)
			return v, v, v
		}), want{
			0.00174632361, 0.000320880532, 0.0523270588, 0.00143975407, 0.000314648445,
			0.106384177, 0, -40.9800644, 9.510, 117_439_461,
		}},
		{"C", refImage(640, 480, func(x, y int) (uint8, uint8, uint8) {
			a := refA(x, y, 1)
			rg := clamp255(2 * a)
			return rg, rg, clamp255(a + 60)
		}), want{
			0.00244116455, 0.000387579987, 0.0647634565, 0.00197155558, 0.00037803356,
			1.9642239, 0.536468099, -46.921814, 6.539, 195_801_086,
		}},
		{"D", refImage(2048, 1536, func(x, y int) (uint8, uint8, uint8) {
			v := uint8(refA(x, y, 2))
			return v, v, v
		}), want{
			0.00129932664, 0.000310955732, 0.0261635294, 0.00122268426, 0.000309397711,
			0.144848853, 0, -40.8680438, 9.566, 1_203_225_198,
		}},
		{"E", refImage(640, 480, func(int, int) (uint8, uint8, uint8) {
			return 128, 128, 128
		}), want{
			0, 0, 0, 1e-9, 1e-9, 0.262770556, 0, -115.392025, 0, 0,
		}},
		// Resampled by the non-integer ratio 1.5625 to 1024×768.
		{"F", refImage(1600, 1200, func(x, y int) (uint8, uint8, uint8) {
			a := refA(x, y, 2)
			return uint8(a), clamp255(a + 10), clamp255(a - 10) //nolint:gosec // a is in 0..255
		}), want{
			0.00193048378, 0.000166922077, 0.0116282353, 0.00191534455, 0.000166614319,
			0.305494051, 0, -40.7327979, 9.634, 734_390_856,
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if c.want.sum != 0 {
				if got := channelSum(c.img); got != c.want.sum {
					t.Fatalf("channel sum = %d, want %d (generator differs from the spec)", got, c.want.sum)
				}
			}
			f, ok := Measure(c.img)
			if !ok {
				t.Fatal("Measure: ok = false")
			}
			check := func(name string, got, want float64) {
				t.Helper()
				if want == 0 {
					if math.Abs(got) > 1e-6 {
						t.Errorf("%s = %.12g, want 0", name, got)
					}
					return
				}
				if math.Abs(got-want) > 1e-6*math.Abs(want) {
					t.Errorf("%s = %.12g, want %.12g (rel err %.3g)", name, got, want, math.Abs(got-want)/math.Abs(want))
				}
			}
			check("var_1", f.Var1, c.want.var1)
			check("var_2", f.Var2, c.want.var2)
			check("noise", f.Noise, c.want.noise)
			check("dn_1", f.DN1, c.want.dn1)
			check("dn_2", f.DN2, c.want.dn2)
			check("midgrey_ev", f.MidGreyEV, c.want.midgrey)
			check("clip_hi", f.ClipHi, c.want.clip)
			raw := Raw(f)
			if math.Abs(raw-c.want.raw) > 1e-6 {
				t.Errorf("raw = %.10g, want %.10g", raw, c.want.raw)
			}
			if d := Display(raw); math.Abs(d-c.want.display) > 1e-3 {
				t.Errorf("display = %.6g, want %.6g", d, c.want.display)
			}
		})
	}
}

// kernelEnergy is the squared norm of the response of the discrete
// Laplacian of Gaussian at sigma to a unit impulse.
func kernelEnergy(sigma float64) float64 {
	const n = 129
	p := newPlane(n, n)
	p.v[64*n+64] = 1
	r := laplacian4(gaussianBlur(p, sigma))
	var s float64
	for _, v := range r.v {
		s += v * v
	}
	return s
}

func TestKernelEnergy(t *testing.T) {
	for _, c := range []struct {
		sigma, want float64
	}{{1, k1}, {2, k2}} {
		if got := kernelEnergy(c.sigma); math.Abs(got-c.want) > 1e-12 {
			t.Errorf("kernel energy at sigma %g = %.17g, want %.17g", c.sigma, got, c.want)
		}
	}
	if k1 != 0.11196346895601238 || k2 != 0.0022760448925639087 {
		t.Errorf("constants changed: k1 %v, k2 %v", k1, k2)
	}
}

func finiteFeatures(f Features) bool {
	for _, v := range []float64{f.Var1, f.Var2, f.Noise, f.DN1, f.DN2, f.MidGreyEV, f.ClipHi} {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return false
		}
	}
	return true
}

func TestMeasure_NeverNaN(t *testing.T) {
	uniform := func(w, h int, v uint8) *image.NRGBA {
		return refImage(w, h, func(int, int) (uint8, uint8, uint8) { return v, v, v })
	}
	for _, v := range []uint8{0, 255} {
		f, ok := Measure(uniform(640, 480, v))
		if !ok || !finiteFeatures(f) {
			t.Fatalf("uniform %d: ok %v, features %+v", v, ok, f)
		}
		if r := Raw(f); math.IsNaN(r) || math.IsInf(r, 0) {
			t.Errorf("uniform %d: raw = %v", v, r)
		}
	}
	for _, sz := range [][2]int{{1, 1}, {15, 200}, {16, 200}, {200, 16}} {
		f, ok := Measure(uniform(sz[0], sz[1], 100))
		if ok || f != (Features{}) {
			t.Errorf("%dx%d: ok %v, features %+v; want not measurable", sz[0], sz[1], ok, f)
		}
	}
	img := refImage(17, 200, func(x, y int) (uint8, uint8, uint8) {
		v := uint8(refA(x, y, 1))
		return v, v, v
	})
	f, ok := Measure(img)
	if !ok || !finiteFeatures(f) {
		t.Fatalf("17x200: ok %v, features %+v", ok, f)
	}
	if r := Raw(f); math.IsNaN(r) || math.IsInf(r, 0) {
		t.Errorf("17x200: raw = %v", r)
	}
}

// blurred returns img convolved with a Gaussian of sigma, per channel,
// rounded back to 8 bits.
func blurred(img *image.NRGBA, sigma float64) *image.NRGBA {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	out := image.NewNRGBA(b)
	copy(out.Pix, img.Pix)
	for c := range 3 {
		p := newPlane(w, h)
		for i := range p.v {
			p.v[i] = float64(img.Pix[i*4+c])
		}
		g := gaussianBlur(p, sigma)
		for i, v := range g.v {
			out.Pix[i*4+c] = uint8(math.Min(math.Max(math.Round(v), 0), 255))
		}
	}
	return out
}

func TestMeasure_Orders(t *testing.T) {
	a := refImageA()
	fa, ok := Measure(a)
	if !ok {
		t.Fatal("A not measurable")
	}
	rawA := Raw(fa)

	noisy := refImage(640, 480, func(x, y int) (uint8, uint8, uint8) {
		i := a.PixOffset(x, y)
		v := clamp255(int(a.Pix[i]) + int(refNoise(int64(x), int64(y), 81)) - 40)
		return v, v, v
	})
	doubled := refImage(640, 480, func(x, y int) (uint8, uint8, uint8) {
		i := a.PixOffset(x, y)
		return clamp255(2 * int(a.Pix[i])), clamp255(2 * int(a.Pix[i+1])), clamp255(2 * int(a.Pix[i+2]))
	})
	for name, img := range map[string]*image.NRGBA{
		"blurred": blurred(a, 2),
		"noisy":   noisy,
		"doubled": doubled,
	} {
		f, ok := Measure(img)
		if !ok {
			t.Fatalf("%s not measurable", name)
		}
		if r := Raw(f); r >= rawA {
			t.Errorf("%s: raw %.4f, not below A's %.4f", name, r, rawA)
		}
	}
}

func TestDisplay(t *testing.T) {
	for _, c := range []struct{ raw, want float64 }{
		{-60, 0}, {-40, 10}, {-50, 5}, {-200, 0}, {0, 10},
	} {
		if got := Display(c.raw); got != c.want {
			t.Errorf("Display(%v) = %v, want %v", c.raw, got, c.want)
		}
	}
}

// BenchmarkMeasure1600 measures one preview-sized photo (1600×1067).
func BenchmarkMeasure1600(b *testing.B) {
	img := refImage(1600, 1067, func(x, y int) (uint8, uint8, uint8) {
		v := refA(x, y, 2)
		return uint8(v), clamp255(v + int(refNoise(int64(x), int64(y), 21)) - 10), clamp255(v / 2)
	})
	for b.Loop() {
		if _, ok := Measure(img); !ok {
			b.Fatal("not measurable")
		}
	}
}
