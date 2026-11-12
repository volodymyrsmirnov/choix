//go:build darwin

package meta

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"log/slog"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/volodymyrsmirnov/choix/internal/platform/darwin/avmedia"
	"github.com/volodymyrsmirnov/choix/internal/platform/darwin/imageio"
	"github.com/volodymyrsmirnov/choix/internal/rawpreview"
	"github.com/volodymyrsmirnov/choix/internal/scanner"
)

var (
	parityAll = flag.Bool("parity.all", false,
		"TestParity_Corpus: check every corpus file instead of 40 per extension")
	parityVerbose = flag.Bool("parity.verbose", false,
		"TestParity_Corpus: developer opt-in. WARNING: prints file paths and metadata values (serials, dates, GPS) of the corpus; never paste its output into a public place (issue, PR, commit, document, log)")
)

// paritySample is the number of files checked per extension.
const paritySample = 40

// parityFile is one corpus file chosen for the parity check.
type parityFile struct {
	abs, rel string
	label    string // "folder-N/file-N": never a name from the corpus
}

// TestParity_Corpus compares, on the owner's corpus, the record exiftool
// writes (v1) with the record the native reader writes, both through Parse.
// It is opt-in: it skips unless CHOIX_UAT_ROOT is set to an existing folder
// (see parityRoot; there is no default). Originals are only read, in place;
// nothing is written under the corpus.
//
// The output never identifies the corpus (R22): a difference is reported as
// "<ext> folder-N/file-N: <field> differs", without values, unless
// -parity.verbose is given on the owner's terminal.
func TestParity_Corpus(t *testing.T) {
	exiftool, err := exec.LookPath("exiftool")
	if err != nil {
		t.Skip("exiftool not installed")
	}
	root, skip := parityRoot()
	if skip != "" {
		t.Skip(skip)
	}

	// The reader's warnings name files; keep them out of the output.
	if !*parityVerbose {
		prev := slog.Default()
		slog.SetDefault(slog.New(slog.DiscardHandler))
		t.Cleanup(func() { slog.SetDefault(prev) })
	}

	byExt, err := parityCollect(root)
	if err != nil {
		t.Fatalf("walk corpus: %v", redact(err))
	}
	exts := make([]string, 0, len(byExt))
	for ext := range byExt {
		exts = append(exts, ext)
	}
	slices.Sort(exts)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	reader := &NativeReader{Stills: imageio.New(), Video: avmedia.New()}

	differences := 0
	for _, ext := range exts {
		files := byExt[ext]
		v1, err := parityExifTool(ctx, exiftool, files)
		if err != nil {
			t.Errorf("%s: exiftool failed: %v", ext, redact(err))
			continue
		}
		for _, f := range files {
			for _, d := range parityCompare(ctx, reader, f, v1[f.abs]) {
				differences++
				if *parityVerbose {
					t.Errorf("%s %s (%s): %s", ext, f.label, f.rel, d.verbose)
				} else {
					t.Errorf("%s %s: %s differs", ext, f.label, d.field)
				}
			}
		}
	}
	t.Logf("parity: %d extension(s) checked, %d difference(s)", len(exts), differences)
}

// parityRootEnv names the folder of photos TestParity_Corpus reads. It has
// no default: the test runs only when it is set to an existing folder.
const parityRootEnv = "CHOIX_UAT_ROOT"

// parityEnableHint is the skip message's instruction; it names no path.
const parityEnableHint = "set " + parityRootEnv + " to a folder of photos to run the exiftool parity comparison"

// parityRoot returns the corpus folder from CHOIX_UAT_ROOT, or a skip
// message when the variable is unset, empty or not an existing folder.
// The message never contains the variable's value.
func parityRoot() (root, skip string) {
	root = os.Getenv(parityRootEnv)
	if root == "" {
		return "", parityEnableHint
	}
	if st, err := os.Stat(root); err != nil || !st.IsDir() {
		return "", parityRootEnv + " is not an existing folder; " + parityEnableHint
	}
	return root, ""
}

// TestParityRoot pins how TestParity_Corpus finds its corpus: only from
// CHOIX_UAT_ROOT, with no default folder, and a skip message that names no
// path. It never reads a real archive: the one folder it accepts is a
// t.TempDir.
func TestParityRoot(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "not-a-folder")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name     string
		value    string
		unset    bool
		wantRoot string
	}{
		{name: "unset", unset: true},
		{name: "empty", value: ""},
		{name: "missing", value: filepath.Join(dir, "missing")},
		{name: "file", value: file},
		{name: "folder", value: dir, wantRoot: dir},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv(parityRootEnv, tt.value) // restored after the subtest
			if tt.unset {
				if err := os.Unsetenv(parityRootEnv); err != nil {
					t.Fatal(err)
				}
			}
			root, skip := parityRoot()
			if root != tt.wantRoot {
				t.Errorf("root = %q, want %q", root, tt.wantRoot)
			}
			if tt.wantRoot != "" {
				if skip != "" {
					t.Errorf("skip = %q, want none", skip)
				}
				return
			}
			if !strings.Contains(skip, parityEnableHint) {
				t.Errorf("skip = %q, want it to contain %q", skip, parityEnableHint)
			}
			if strings.ContainsAny(skip, `/\~`) || (tt.value != "" && strings.Contains(skip, tt.value)) {
				t.Errorf("skip = %q names a path", skip)
			}
		})
	}
}

// parityCollect walks root and returns the chosen media files by lower-case
// extension: every file with -parity.all, else up to paritySample per
// extension, sorted by path and evenly spaced. Hidden directories (.choix)
// and picks directories are skipped. Labels number folders and files in
// path order.
func parityCollect(root string) (map[string][]parityFile, error) {
	all := map[string][]string{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		name := d.Name()
		if d.IsDir() {
			if path != root && (strings.HasPrefix(name, ".") || strings.EqualFold(name, "picks")) {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() || strings.HasPrefix(name, ".") {
			return nil
		}
		if _, ok := scanner.DetectFromExt(name); ok {
			ext := strings.ToLower(filepath.Ext(name))
			all[ext] = append(all[ext], path)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	chosen := map[string][]string{}
	var dirs []string
	for ext, paths := range all {
		slices.Sort(paths)
		if !*parityAll && len(paths) > paritySample {
			picked := make([]string, 0, paritySample)
			for i := range paritySample {
				picked = append(picked, paths[i*len(paths)/paritySample])
			}
			paths = picked
		}
		chosen[ext] = paths
		for _, p := range paths {
			dirs = append(dirs, filepath.Dir(p))
		}
	}
	slices.Sort(dirs)
	dirs = slices.Compact(dirs)
	folder := make(map[string]int, len(dirs))
	for i, d := range dirs {
		folder[d] = i + 1
	}

	// Number files within each folder in path order, across extensions.
	var every []string
	for _, paths := range chosen {
		every = append(every, paths...)
	}
	slices.Sort(every)
	fileNo := make(map[string]int, len(every))
	perFolder := map[string]int{}
	for _, p := range every {
		perFolder[filepath.Dir(p)]++
		fileNo[p] = perFolder[filepath.Dir(p)]
	}

	out := make(map[string][]parityFile, len(chosen))
	for ext, paths := range chosen {
		for _, p := range paths {
			rel, err := filepath.Rel(root, p)
			if err != nil {
				rel = p
			}
			out[ext] = append(out[ext], parityFile{
				abs:   p,
				rel:   rel,
				label: fmt.Sprintf("folder-%d/file-%d", folder[filepath.Dir(p)], fileNo[p]),
			})
		}
	}
	return out, nil
}

// parityExifTool runs `exiftool -j -G -n` over files in batches and returns
// each file's record, re-wrapped as the one-element array v1 stored.
func parityExifTool(ctx context.Context, exiftool string, files []parityFile) (map[string][]byte, error) {
	out := make(map[string][]byte, len(files))
	for batch := range slices.Chunk(files, 50) {
		args := []string{"-j", "-G", "-n", "--"}
		for _, f := range batch {
			args = append(args, f.abs)
		}
		cmd := exec.CommandContext(ctx, exiftool, args...) //nolint:gosec // exiftool from PATH, test only
		var stdout bytes.Buffer
		cmd.Stdout = &stdout
		runErr := cmd.Run() // exiftool exits non-zero when one file of the batch is unreadable
		var recs []map[string]any
		if err := json.Unmarshal(stdout.Bytes(), &recs); err != nil {
			return nil, errors.Join(fmt.Errorf("decode exiftool output: %w", err), runErr)
		}
		for _, r := range recs {
			src, _ := r["SourceFile"].(string)
			b, err := json.Marshal([]map[string]any{r})
			if err != nil {
				return nil, err
			}
			out[src] = b
		}
	}
	return out, nil
}

// parityDiff is one field that differs; verbose carries the values.
type parityDiff struct{ field, verbose string }

// parityCompare reads f natively and compares the parsed result with the
// parsed exiftool record v1.
func parityCompare(ctx context.Context, reader *NativeReader, f parityFile, v1 []byte) []parityDiff {
	if v1 == nil {
		return []parityDiff{{"exiftool record", "exiftool returned no record"}}
	}
	want, err := Parse(v1)
	if err != nil {
		return []parityDiff{{"exiftool record", "parse: " + err.Error()}}
	}
	native, err := reader.Read(ctx, f.abs)
	if err != nil {
		return []parityDiff{{"native read", "native read: " + err.Error()}}
	}
	got, err := Parse(native)
	if err != nil {
		return []parityDiff{{"native record", "parse: " + err.Error()}}
	}

	// C34: a DNG's width and height are those of the preview that decodes.
	if rawpreview.Kind(f.abs) == "dng" {
		if p, err := rawpreview.Embedded(f.abs, rawpreview.Usable(ctx, reader.Stills)); err == nil {
			want.Width, want.Height = p.Width, p.Height
		}
	}

	var diffs []parityDiff
	add := func(field string, a, b any) {
		diffs = append(diffs, parityDiff{field, fmt.Sprintf("%s: exiftool=%v native=%v", field, a, b)})
	}
	if a, b := DeviceKey(want), DeviceKey(got); a != b {
		add("DeviceKey", a, b)
	}
	wc, wok := Captured(want)
	gc, gok := Captured(got)
	if wc != gc || wok != gok {
		add("Captured", fmt.Sprint(wc, wok), fmt.Sprint(gc, gok))
	}
	if want.Width != got.Width {
		add("Width", want.Width, got.Width)
	}
	if want.Height != got.Height {
		add("Height", want.Height, got.Height)
	}
	if want.ISO != got.ISO {
		add("ISO", want.ISO, got.ISO)
	}
	if math.Abs(want.Aperture-got.Aperture) > 1e-6 {
		add("Aperture", want.Aperture, got.Aperture)
	}
	if want.Shutter != got.Shutter {
		add("Shutter", want.Shutter, got.Shutter)
	}
	if math.Abs(want.FocalLength-got.FocalLength) > 1e-6 {
		add("FocalLength", want.FocalLength, got.FocalLength)
	}
	if want.HasGPS != got.HasGPS {
		add("HasGPS", want.HasGPS, got.HasGPS)
	} else if math.Abs(want.GPSLatitude-got.GPSLatitude) > 1e-6 || math.Abs(want.GPSLongitude-got.GPSLongitude) > 1e-6 {
		add("GPS", fmt.Sprint(want.GPSLatitude, want.GPSLongitude), fmt.Sprint(got.GPSLatitude, got.GPSLongitude))
	}
	return diffs
}

// redact keeps only an error's type of failure, never the paths in its text.
func redact(err error) string {
	var pe *fs.PathError
	if errors.As(err, &pe) {
		return pe.Op + ": " + pe.Err.Error()
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return fmt.Sprintf("exit status %d", ee.ExitCode())
	}
	return "error (details only with -parity.verbose)"
}
