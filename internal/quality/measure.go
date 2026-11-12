package quality

import (
	"image"
	"image/color"
	"math"
)

// ScorerID identifies the formula below. Rows stamped with another id are
// pending again and their old scores are hidden. Changing any step,
// constant or weight of Measure or Raw must change it.
const ScorerID = "choix-sne-1024/v1"

// MinShortSide is the shortest preview side the scorer rates; a smaller
// preview is stamped unrated.
const MinShortSide = 384

// workLong is the long side of the working copy (§6.2 step 1).
const workLong = 1024

// minMeasurable is the smallest working-copy side Measure accepts: at
// σ = 2 the variance skips a border of 8 pixels on each side.
const minMeasurable = 17

// k1 and k2 are the squared norms of the discrete Laplacian-of-Gaussian
// kernel at σ = 1 and σ = 2: the variance white noise of σ = 1 adds to the
// response (TestKernelEnergy recomputes them).
const (
	k1 = 0.11196346895601238
	k2 = 0.0022760448925639087
)

// Features are the measurements of one photo (spec §6.2).
type Features struct {
	Var1, Var2 float64 // variance of the LoG response at σ = 1 and σ = 2
	Noise      float64 // robust noise sigma
	DN1, DN2   float64 // noise-corrected sharpness
	MidGreyEV  float64 // distance of the mean log luminance from mid-grey, in EV
	ClipHi     float64 // share of pixels whose largest channel is ≥ 250/255
}

// Measure computes the features of img (§6.2). ok is false, and f is the
// zero value, when a side of the working copy is under 17 px.
func Measure(img image.Image) (f Features, ok bool) {
	w, h, pix := rgb8(img)
	w, h, pix = workingCopy(w, h, pix)
	if w < minMeasurable || h < minMeasurable {
		return Features{}, false
	}

	y := newPlane(w, h)
	for i := range y.v {
		r := float64(pix[3*i]) / 255
		g := float64(pix[3*i+1]) / 255
		b := float64(pix[3*i+2]) / 255
		y.v[i] = r*0.2126 + g*0.7152 + b*0.0722
	}

	g1 := gaussianBlur(y, 1)
	f.Var1 = interiorVariance(laplacian4(g1), 5)
	g2 := gaussianBlur(y, 2)
	f.Var2 = interiorVariance(laplacian4(g2), 8)

	f.Noise = noiseSigma(y, g2)
	n2 := f.Noise * f.Noise
	f.DN1 = math.Max(f.Var1-k1*n2, 1e-9)
	f.DN2 = math.Max(f.Var2-k2*n2, 1e-9)

	f.MidGreyEV, f.ClipHi = exposure(pix)
	return f, true
}

// Raw is the quality score stored in files.quality_raw (§6.2 step 7).
func Raw(f Features) float64 {
	return 3*math.Log(f.DN1) + 3*math.Log(f.DN2) - math.Log(f.Noise+1e-4) - f.MidGreyEV - 10*f.ClipHi
}

// Display maps a raw score to the 0–10 badge (§6.4). Recalibrate here,
// never by rescoring.
func Display(raw float64) float64 {
	return math.Min(math.Max((raw+60)/2, 0), 10)
}

// plane is a row-major float64 image.
type plane struct {
	w, h int
	v    []float64
}

func newPlane(w, h int) plane {
	return plane{w: w, h: h, v: make([]float64, w*h)}
}

// reflect maps an index outside [0, n) back inside by reflecting at the
// edge without repeating the edge sample (numpy's "reflect"). It handles
// indexes at most n-1 outside.
func reflect(i, n int) int {
	if i < 0 {
		return -i
	}
	if i >= n {
		return 2*(n-1) - i
	}
	return i
}

// gauss1d is the normalised Gaussian kernel of radius ceil(3σ).
func gauss1d(sigma float64) []float64 {
	r := max(1, int(math.Ceil(3*sigma)))
	k := make([]float64, 2*r+1)
	var sum float64
	for i := range k {
		x := float64(i-r) / sigma
		k[i] = math.Exp(-0.5 * x * x)
		sum += k[i]
	}
	for i := range k {
		k[i] /= sum
	}
	return k
}

// gaussianBlur convolves p with a separable Gaussian, rows first, with
// reflected borders.
func gaussianBlur(p plane, sigma float64) plane {
	k := gauss1d(sigma)
	r := len(k) / 2
	w, h := p.w, p.h

	tmp := newPlane(w, h)
	for y := range h {
		row := p.v[y*w : (y+1)*w]
		out := tmp.v[y*w : (y+1)*w]
		for x := range w {
			var s float64
			if x >= r && x+r < w {
				src := row[x-r : x+r+1]
				for i, kv := range k {
					s += kv * src[i]
				}
			} else {
				for i, kv := range k {
					s += kv * row[reflect(x-r+i, w)]
				}
			}
			out[x] = s
		}
	}

	dst := newPlane(w, h)
	rows := make([][]float64, len(k))
	for y := range h {
		for i := range k {
			yy := reflect(y-r+i, h)
			rows[i] = tmp.v[yy*w : (yy+1)*w]
		}
		out := dst.v[y*w : (y+1)*w]
		for x := range w {
			var s float64
			for i, kv := range k {
				s += kv * rows[i][x]
			}
			out[x] = s
		}
	}
	return dst
}

// laplacian4 is up + down + left + right − 4·centre, with reflected borders.
func laplacian4(g plane) plane {
	w, h := g.w, g.h
	out := newPlane(w, h)
	for y := range h {
		up := reflect(y-1, h) * w
		down := reflect(y+1, h) * w
		row := y * w
		for x := range w {
			left := reflect(x-1, w)
			right := reflect(x+1, w)
			out.v[row+x] = g.v[up+x] + g.v[down+x] + g.v[row+left] + g.v[row+right] - 4*g.v[row+x]
		}
	}
	return out
}

// interiorVariance is the population variance of p without a border of b
// pixels on every side.
func interiorVariance(p plane, b int) float64 {
	n := float64((p.w - 2*b) * (p.h - 2*b))
	var sum float64
	for y := b; y < p.h-b; y++ {
		for _, v := range p.v[y*p.w+b : y*p.w+p.w-b] {
			sum += v
		}
	}
	mean := sum / n
	var ss float64
	for y := b; y < p.h-b; y++ {
		for _, v := range p.v[y*p.w+b : y*p.w+p.w-b] {
			d := v - mean
			ss += d * d
		}
	}
	return ss / n
}

// noiseMask is Immerkær's 3×3 noise-estimation mask (white-noise gain 6).
var noiseMask = [3][3]float64{{1, -2, 1}, {-2, 4, -2}, {1, -2, 1}}

// noiseSigma is §6.2 step 4: the median absolute response of the noise
// mask over flat, unclipped pixels, scaled to a Gaussian sigma.
func noiseSigma(y, g2 plane) float64 {
	w, h := y.w, y.h
	gm := gradientMagnitude(g2)

	okCount := 0
	for _, v := range y.v {
		if v > 0.03 && v < 0.97 {
			okCount++
		}
	}
	all := okCount < 1000
	isOK := func(i int) bool { return all || (y.v[i] > 0.03 && y.v[i] < 0.97) }

	sel := make([]float64, 0, w*h)
	for i, v := range gm.v {
		if isOK(i) {
			sel = append(sel, v)
		}
	}
	thr := percentile(sel, 0.30)

	flat := sel[:0]
	for yy := range h {
		rows := [3]int{reflect(yy-1, h) * w, yy * w, reflect(yy+1, h) * w}
		for x := range w {
			i := yy*w + x
			if !isOK(i) || gm.v[i] > thr {
				continue
			}
			cols := [3]int{reflect(x-1, w), x, reflect(x+1, w)}
			var c float64
			for a := range 3 {
				for b := range 3 {
					c += noiseMask[a][b] * y.v[rows[a]+cols[b]]
				}
			}
			flat = append(flat, math.Abs(c))
		}
	}
	return 1.4826 * median(flat) / 6
}

// gradientMagnitude is |∇p| with central differences inside and one-sided
// differences at the edges (numpy's gradient).
func gradientMagnitude(p plane) plane {
	w, h := p.w, p.h
	out := newPlane(w, h)
	diff := func(at func(int) float64, i, n int) float64 {
		switch i {
		case 0:
			return at(1) - at(0)
		case n - 1:
			return at(n-1) - at(n-2)
		default:
			return (at(i+1) - at(i-1)) / 2
		}
	}
	for y := range h {
		for x := range w {
			gy := diff(func(j int) float64 { return p.v[j*w+x] }, y, h)
			gx := diff(func(j int) float64 { return p.v[y*w+j] }, x, w)
			out.v[y*w+x] = math.Hypot(gx, gy)
		}
	}
	return out
}

// percentile is numpy's default (linear) percentile of a at q in [0, 1].
// It reorders a.
func percentile(a []float64, q float64) float64 {
	n := len(a)
	pos := q * float64(n-1)
	lo := int(math.Floor(pos))
	t := pos - float64(lo)
	va := nthSmallest(a, lo)
	if lo+1 >= n {
		return va
	}
	vb := minOf(a[lo+1:])
	d := vb - va
	if t >= 0.5 {
		return vb - d*(1-t)
	}
	return va + d*t
}

// median is the middle value of a, or the mean of the two middle values
// for an even count. It reorders a.
func median(a []float64) float64 {
	n := len(a)
	if n%2 == 1 {
		return nthSmallest(a, n/2)
	}
	lo := nthSmallest(a, n/2-1)
	return (lo + minOf(a[n/2:])) / 2
}

func minOf(a []float64) float64 {
	m := a[0]
	for _, v := range a[1:] {
		m = min(m, v)
	}
	return m
}

// nthSmallest reorders a so that a[k] is its k-th smallest value, every
// element before it is ≤ a[k] and every element after it is ≥ a[k], and
// returns a[k]. Three-way partitioning keeps runs of equal values linear.
func nthSmallest(a []float64, k int) float64 {
	lo, hi := 0, len(a)-1
	for lo < hi {
		// Median of three as the pivot.
		mid := lo + (hi-lo)/2
		x, y, z := a[lo], a[mid], a[hi]
		pivot := max(min(x, y), min(max(x, y), z))

		lt, i, gt := lo, lo, hi
		for i <= gt {
			switch {
			case a[i] < pivot:
				a[lt], a[i] = a[i], a[lt]
				lt++
				i++
			case a[i] > pivot:
				a[i], a[gt] = a[gt], a[i]
				gt--
			default:
				i++
			}
		}
		switch {
		case k < lt:
			hi = lt - 1
		case k > gt:
			lo = gt + 1
		default:
			return a[k]
		}
	}
	return a[k]
}

// srgbLinear maps an 8-bit sRGB code value to linear light.
var srgbLinear = func() (t [256]float64) {
	for c := range t {
		v := float64(c) / 255
		if v <= 0.04045 {
			t[c] = v / 12.92
		} else {
			t[c] = math.Pow((v+0.055)/1.055, 2.4)
		}
	}
	return t
}()

// exposure is §6.2 step 6 over interleaved 8-bit RGB.
func exposure(pix []uint8) (midGreyEV, clipHi float64) {
	n := len(pix) / 3
	var sum float64
	clipped := 0
	const hi = 250.0 / 255
	for i := range n {
		r, g, b := pix[3*i], pix[3*i+1], pix[3*i+2]
		yl := srgbLinear[r]*0.2126 + srgbLinear[g]*0.7152 + srgbLinear[b]*0.0722
		sum += math.Log2(yl + 1e-4)
		if float64(max(r, g, b))/255 >= hi {
			clipped++
		}
	}
	midGreyEV = math.Abs(sum/float64(n) - math.Log2(0.18))
	clipHi = float64(clipped) / float64(n)
	return midGreyEV, clipHi
}

// rgb8 returns img as interleaved 8-bit RGB (alpha dropped).
func rgb8(img image.Image) (w, h int, pix []uint8) {
	b := img.Bounds()
	w, h = b.Dx(), b.Dy()
	pix = make([]uint8, 3*w*h)
	switch m := img.(type) {
	case *image.NRGBA:
		for y := range h {
			src := m.Pix[m.PixOffset(b.Min.X, b.Min.Y+y):]
			for x := range w {
				copy(pix[3*(y*w+x):3*(y*w+x)+3], src[4*x:4*x+3])
			}
		}
	case *image.YCbCr:
		for y := range h {
			for x := range w {
				yi := m.YOffset(b.Min.X+x, b.Min.Y+y)
				ci := m.COffset(b.Min.X+x, b.Min.Y+y)
				r, g, bb := color.YCbCrToRGB(m.Y[yi], m.Cb[ci], m.Cr[ci])
				o := 3 * (y*w + x)
				pix[o], pix[o+1], pix[o+2] = r, g, bb
			}
		}
	case *image.Gray:
		for y := range h {
			for x := range w {
				v := m.Pix[m.PixOffset(b.Min.X+x, b.Min.Y+y)]
				o := 3 * (y*w + x)
				pix[o], pix[o+1], pix[o+2] = v, v, v
			}
		}
	default:
		for y := range h {
			for x := range w {
				c := color.NRGBAModel.Convert(img.At(b.Min.X+x, b.Min.Y+y)).(color.NRGBA)
				o := 3 * (y*w + x)
				pix[o], pix[o+1], pix[o+2] = c.R, c.G, c.B
			}
		}
	}
	return w, h, pix
}

// workingCopy resamples interleaved RGB whose long side is over workLong
// down to long side workLong (§6.2 step 1). Smaller images are returned
// as they are.
func workingCopy(w, h int, pix []uint8) (int, int, []uint8) {
	long := max(w, h)
	if long <= workLong {
		return w, h, pix
	}
	s := float64(workLong) / float64(long)
	nw := max(1, int(math.RoundToEven(float64(w)*s)))
	nh := max(1, int(math.RoundToEven(float64(h)*s)))
	return nw, nh, boxResample(w, h, pix, nw, nh)
}

// precisionBits is the fixed-point precision of the 8-bit resampler.
const precisionBits = 32 - 8 - 2

// boxCoeffs are the fixed-point weights of an area (box) filter from in
// samples to out samples: for output i, the inputs start..start+len(w[i]).
type boxCoeffs struct {
	start []int
	w     [][]int64
}

// newBoxCoeffs computes area-filter weights the way Pillow's Image.BOX
// resize does, so a working copy matches the one the formula was measured
// on (§6.3 image D).
func newBoxCoeffs(in, out int) boxCoeffs {
	scale := float64(in) / float64(out)
	filterScale := max(scale, 1)
	support := 0.5 * filterScale
	c := boxCoeffs{start: make([]int, out), w: make([][]int64, out)}
	for i := range out {
		center := (float64(i) + 0.5) * scale
		ss := 1 / filterScale
		lo := max(int(center-support+0.5), 0)
		hi := min(int(center+support+0.5), in)
		ws := make([]float64, hi-lo)
		var total float64
		for x := range ws {
			v := (float64(x+lo) - center + 0.5) * ss
			if v > -0.5 && v <= 0.5 {
				ws[x] = 1
			}
			total += ws[x]
		}
		fixed := make([]int64, len(ws))
		for x, v := range ws {
			if total != 0 {
				v /= total
			}
			fixed[x] = int64(0.5 + v*(1<<precisionBits))
		}
		c.start[i], c.w[i] = lo, fixed
	}
	return c
}

func clip8(v int64) uint8 {
	switch {
	case v >= 1<<precisionBits<<8:
		return 255
	case v <= 0:
		return 0
	default:
		return uint8(v >> precisionBits)
	}
}

// boxResample is an 8-bit area resample, horizontal pass first, each pass
// rounded to 8 bits (half up).
func boxResample(w, h int, pix []uint8, nw, nh int) []uint8 {
	cx := newBoxCoeffs(w, nw)
	tmp := make([]uint8, 3*nw*h)
	for y := range h {
		for x := range nw {
			for ch := range 3 {
				s := int64(1) << (precisionBits - 1)
				for i, k := range cx.w[x] {
					s += int64(pix[3*(y*w+cx.start[x]+i)+ch]) * k
				}
				tmp[3*(y*nw+x)+ch] = clip8(s)
			}
		}
	}
	cy := newBoxCoeffs(h, nh)
	out := make([]uint8, 3*nw*nh)
	for y := range nh {
		for x := range nw {
			for ch := range 3 {
				s := int64(1) << (precisionBits - 1)
				for i, k := range cy.w[y] {
					s += int64(tmp[3*((cy.start[y]+i)*nw+x)+ch]) * k
				}
				out[3*(y*nw+x)+ch] = clip8(s)
			}
		}
	}
	return out
}
