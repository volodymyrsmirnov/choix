package thumb

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/volodymyrsmirnov/choix/internal/deps"
)

// probeDuration returns the duration of a video file in seconds by parsing
// the "Duration:" line from `ffmpeg -i src` stderr output. This avoids a
// separate ffprobe dependency while remaining accurate enough for evenly-
// spaced frame sampling.
func probeDuration(ctx context.Context, ffmpeg *deps.Runner, srcAbs string) (float64, error) {
	// ffmpeg -i <src> always exits non-zero (no output file), so we use
	// exec.Command directly to capture stderr without treating non-zero exit
	// as an error. ffmpeg.Path is the resolved tool path (from LookPath or
	// the support dir), not user input.
	cmd := exec.CommandContext(ctx, ffmpeg.Path, "-i", srcAbs) //nolint:gosec // ffmpeg.Path is resolved tool path, srcAbs is a validated file path
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	_ = cmd.Run() // expected to fail (no output file specified)

	return parseDurationFromFFmpegOutput(stderr.String())
}

// durationRe matches lines like "  Duration: 00:01:23.45, start: ..."
var durationRe = regexp.MustCompile(`Duration:\s+(\d+):(\d+):(\d+(?:\.\d+)?)`)

// parseDurationFromFFmpegOutput extracts the duration in seconds from
// ffmpeg's -i stderr output.
func parseDurationFromFFmpegOutput(output string) (float64, error) {
	scanner := bufio.NewScanner(strings.NewReader(output))
	for scanner.Scan() {
		line := scanner.Text()
		m := durationRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		h, _ := strconv.ParseFloat(m[1], 64)
		min, _ := strconv.ParseFloat(m[2], 64)
		sec, _ := strconv.ParseFloat(m[3], 64)
		total := h*3600 + min*60 + sec
		if total <= 0 {
			return 0, fmt.Errorf("parsed non-positive duration from ffmpeg output")
		}
		return total, nil
	}
	return 0, fmt.Errorf("duration not found in ffmpeg output")
}

// BuildVideoKeyframes extracts five evenly-spaced keyframes from srcAbs and
// writes them to individual JPEG files in a fresh temp directory created
// inside filepath.Dir(dstPattern). It returns the absolute paths of the five
// frames, sorted lexicographically.
//
// Strategy: probe duration via `ffmpeg -i` stderr, then run five separate
// `ffmpeg -ss <t> -i src -frames:v 1` invocations at fractional offsets
// (0.0, 0.25, 0.5, 0.75, 0.99 of duration). This is more robust than the
// select filter, which requires the `N` constant that is unsupported in
// many ffmpeg builds.
//
// The caller is responsible for the lifetime of the returned paths; they
// live inside a subdirectory of filepath.Dir(dstPattern).
func BuildVideoKeyframes(ctx context.Context, ffmpeg *deps.Runner, srcAbs, dstPattern string) ([]string, error) {
	if err := ensureDir(dstPattern); err != nil {
		return nil, err
	}

	duration, err := probeDuration(ctx, ffmpeg, srcAbs)
	if err != nil {
		return nil, fmt.Errorf("probe duration: %w", err)
	}

	// Create a fresh temp directory to hold this run's frames. Using a new
	// dir per call means no stale frames from previous runs appear in results.
	tmpDir, err := os.MkdirTemp(filepath.Dir(dstPattern), "keyframes-*")
	if err != nil {
		return nil, fmt.Errorf("mkdirtemp: %w", err)
	}

	const nFrames = 5
	// Offsets are fractions of duration. 0.95 rather than 1.0 avoids seeking
	// past the last encoded frame in short videos (e.g. a 10fps video has its
	// last frame at duration-1/fps, so 0.99*duration overshoots).
	offsets := [nFrames]float64{0.0, 0.25, 0.5, 0.75, 0.95}

	out := make([]string, 0, nFrames)
	for i, frac := range offsets {
		ts := frac * duration
		dst := filepath.Join(tmpDir, fmt.Sprintf("%02d.jpg", i+1))
		args := []string{
			"-y", "-loglevel", "error",
			"-ss", strconv.FormatFloat(ts, 'f', 3, 64),
			"-i", srcAbs,
			"-frames:v", "1",
			"-q:v", "2",
			// yuvj420p is the full-range YUV pixel format expected by the JPEG
			// encoder. Without this, ffmpeg outputs limited-range YUV (yuv420p)
			// which the mjpeg encoder rejects with "Non full-range YUV is
			// non-standard" when strict compliance is enabled.
			"-pix_fmt", "yuvj420p",
			dst,
		}
		if _, runErr := ffmpeg.Run(ctx, args...); runErr != nil {
			return nil, fmt.Errorf("ffmpeg keyframe %d at %.3fs: %w", i+1, ts, runErr)
		}
		absPath, err := filepath.Abs(dst)
		if err != nil {
			return nil, fmt.Errorf("abs %s: %w", dst, err)
		}
		out = append(out, absPath)
	}

	if len(out) == 0 {
		return nil, fmt.Errorf("ffmpeg produced no keyframes for %s", srcAbs)
	}
	sort.Strings(out)
	return out, nil
}

// KeyframesDir returns the per-file folder where video keyframes are cached
// for the given file id. It mirrors CachePath's bucket sharding.
func KeyframesDir(root string, fileID int64) string {
	bucket := fmt.Sprintf("%02x", fileID%256)
	return filepath.Join(root, ".choix", "thumbs", bucket, fmt.Sprintf("%d-keyframes", fileID))
}
