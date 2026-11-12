package e2e_test

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
)

// Synthetic private tree: spaces, quotes, unicode, and a component that is
// a substring of another ("Paris" in "Paris 2019").
const privRoot = "/Volumes/Säm’s Disk/Fotos"

var privPaths = []string{
	`Trip "Paris"/a.jpg`,
	"Paris 2019/Été à Nice/IMG 0001.raf",
	"Paris/Hälsning från Paris 01.jpeg",
	"Ünïcode ‘dir’/sub dir/clip one.mp4",
}

// privFragments are pieces of those names that must never be printed.
var privFragments = []string{
	"Trip", "Paris", "a.jpg", "Été", "Nice", "IMG 0001", "Hälsning", "från", "Ünïcode", "sub dir", "clip one",
	"Säm", "Fotos", "TestUAT123",
}

func testRedactor() *redactor {
	r := newRedactor()
	r.addRoots(privRoot, "/private/var/folders/xy/T/TestUAT123")
	r.addPaths(privPaths...)
	return r
}

func leaks(s string) []string {
	var out []string
	for _, f := range privFragments {
		if strings.Contains(s, f) {
			out = append(out, f)
		}
	}
	return out
}

func TestRedactor_Text(t *testing.T) {
	cases := []struct {
		name     string
		in       string
		want     string // the whole result, when set
		keep     string // a fragment that must survive, when set
		withheld bool
	}{
		{
			name: "the reviewer's case",
			in:   `scanner: upsert Trip "Paris"/a.jpg: constraint failed`,
			want: "scanner: upsert <name>: constraint failed",
		},
		{
			name: "%q-escaped path",
			in:   fmt.Sprintf("upsert %q failed", `Trip "Paris"/a.jpg`),
			keep: "failed",
		},
		{
			name: "nested names with spaces and unicode",
			in:   "thumb: Paris 2019/Été à Nice/IMG 0001.raf: no preview",
			want: "thumb: <name>: no preview",
		},
		{
			name: "a directory prefix only",
			in:   "walk Paris 2019/Été à Nice: busy",
			want: "walk <name>: busy",
		},
		{
			name: "a component that is a substring of another",
			in:   "folders Paris and Paris 2019 differ",
			want: "folders <name> and <name> differ",
		},
		{
			name: "a bare file name",
			in:   "decode IMG 0001.raf: EOF",
			keep: ": EOF",
		},
		{
			name: "quotes and unicode in a directory name",
			in:   "open Ünïcode ‘dir’/sub dir/clip one.mp4 failed",
			keep: "failed",
		},
		{
			name: "a root to the end of the line",
			in:   "cp: " + privRoot + "/Paris/x y.jpg: No such file\ncp: second line",
			want: "cp: <path>\ncp: second line",
		},
		{
			name: "a root inside quotes",
			in:   `sqlite: unable to open "/private/var/folders/xy/T/TestUAT123/001/folder-1/.choix/state.db" (code 14)`,
			want: `sqlite: unable to open "<path>" (code 14)`,
		},
		{
			name:     "an unknown path-like remainder is withheld",
			in:       "load thumb: unknown dir/some file.jpg: bad",
			withheld: true,
		},
		{
			name:     "an unknown absolute path is withheld",
			in:       `decode "/Users/someone/My Photos/x.jpg": EOF`,
			withheld: true,
		},
		{
			name: "an ordinary message is unchanged",
			in:   "database is locked (5)",
			want: "database is locked (5)",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := testRedactor()
			got := r.text(c.in, "*errors.errorString")
			if l := leaks(got); len(l) > 0 {
				t.Fatalf("text = %q; leaks %v", got, l)
			}
			if c.want != "" && got != c.want {
				t.Fatalf("text = %q; want %q", got, c.want)
			}
			if c.keep != "" && !strings.Contains(got, c.keep) {
				t.Fatalf("text = %q; lost %q", got, c.keep)
			}
			if c.withheld {
				if got != "<error text withheld> (*errors.errorString)" || r.withheldCount() != 1 {
					t.Fatalf("text = %q, withheld %d; want it withheld once", got, r.withheldCount())
				}
			} else if r.withheldCount() != 0 {
				t.Fatalf("text = %q was withheld", got)
			}
		})
	}
}

func TestRedactor_ErrTextStructured(t *testing.T) {
	r := testRedactor()
	full := privRoot + "/" + privPaths[1]
	cases := []struct {
		err  error
		want string
	}{
		{fmt.Errorf("walk %s: %w", full, &fs.PathError{Op: "open", Path: full, Err: syscall.ENOENT}), "open: no such file or directory"},
		{&os.LinkError{Op: "rename", Old: full, New: full + ".tmp", Err: syscall.EXDEV}, "rename: cross-device link"},
		{fmt.Errorf("clone %s: %w", full, os.NewSyscallError("clonefile", syscall.ENOTSUP)), "clonefile: operation not supported"},
		{errors.New(`scanner: upsert Trip "Paris"/a.jpg: constraint failed`), "scanner: upsert <name>: constraint failed"},
	}
	for _, c := range cases {
		if got := r.errText(c.err); got != c.want {
			t.Errorf("errText(%T) = %q; want %q", c.err, got, c.want)
		}
	}
	if got := r.errText(fmt.Errorf("wrapped: %w", errors.New("x/y"))); got != "<error text withheld> (*fmt.wrapError)" {
		t.Errorf("errText of a path-like unknown = %q; want it withheld with its type", got)
	}
}

func TestRedactor_ShortNamesOnlyWhenAlone(t *testing.T) {
	r := newRedactor()
	r.addPaths("NY/x1")
	if got := r.text("NYC office: NY closed", "string"); got != "NYC office: <name> closed" {
		t.Fatalf("text = %q", got)
	}
}

func TestWriteReport_Writes(t *testing.T) {
	base := t.TempDir()
	p, err := writeReport(base, "", []byte("one"))
	if err != nil {
		t.Fatalf("writeReport: %v", err)
	}
	if _, err := writeReport(base, "", []byte("two")); err != nil {
		t.Fatalf("writeReport again: %v", err)
	}
	if b, err := os.ReadFile(p); err != nil || string(b) != "two" {
		t.Fatalf("report = %q, %v; want \"two\"", b, err)
	}
	st, err := os.Lstat(p)
	if err != nil || !st.Mode().IsRegular() || st.Mode().Perm() != 0o600 {
		t.Fatalf("report mode = %v, %v; want a regular 0600 file", st.Mode(), err)
	}
	entries, _ := os.ReadDir(filepath.Dir(p))
	if len(entries) != 1 {
		t.Fatalf("report directory holds %d entries; want the report only", len(entries))
	}
}

func TestWriteReport_StaleSymlinkAtReportPath(t *testing.T) {
	base, other := t.TempDir(), t.TempDir()
	sentinel := filepath.Join(other, "original.jpg")
	if err := os.WriteFile(sentinel, []byte("sacred"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(base, reportDirName), 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(base, reportDirName, reportName)
	if err := os.Symlink(sentinel, link); err != nil {
		t.Fatal(err)
	}
	if _, err := writeReport(base, "", []byte("report")); err != nil {
		t.Fatalf("writeReport: %v", err)
	}
	if b, err := os.ReadFile(sentinel); err != nil || string(b) != "sacred" {
		t.Fatalf("the symlink's target = %q, %v; want it untouched", b, err)
	}
	st, err := os.Lstat(link)
	if err != nil || !st.Mode().IsRegular() {
		t.Fatalf("report path mode = %v, %v; want the symlink replaced by a regular file", st.Mode(), err)
	}
}

func TestWriteReport_SymlinkedReportDirRefused(t *testing.T) {
	base, other := t.TempDir(), t.TempDir()
	if err := os.Symlink(other, filepath.Join(base, reportDirName)); err != nil {
		t.Fatal(err)
	}
	if _, err := writeReport(base, "", []byte("report")); err == nil {
		t.Fatal("writeReport through a symlinked report directory succeeded")
	}
	if entries, _ := os.ReadDir(other); len(entries) != 0 {
		t.Fatalf("the symlink's target directory holds %d entries; want none", len(entries))
	}
}

func TestWriteReport_InsideCorpusRefused(t *testing.T) {
	corpus := t.TempDir()
	if _, err := writeReport(corpus, corpus, []byte("report")); err == nil {
		t.Fatal("writeReport inside the corpus root succeeded")
	}
	// Through a symlinked temporary directory too.
	alias := filepath.Join(t.TempDir(), "tmp")
	if err := os.Symlink(corpus, alias); err != nil {
		t.Fatal(err)
	}
	if _, err := writeReport(alias, corpus, []byte("report")); err == nil {
		t.Fatal("writeReport inside the corpus root through a symlink succeeded")
	}
	if entries, _ := os.ReadDir(corpus); len(entries) != 0 {
		t.Fatalf("the corpus root holds %d entries; want none", len(entries))
	}
	// A sibling of the corpus root is fine.
	if _, err := writeReport(t.TempDir(), corpus, []byte("report")); err != nil {
		t.Fatalf("writeReport beside the corpus: %v", err)
	}
}

func TestExpectedRawWidth(t *testing.T) {
	cases := []struct {
		name                      string
		w, h, orient, limit, want int
	}{
		{"wide preview, landscape", 1920, 1280, 1, 1600, 1600},
		{"wide preview, thumb tier", 1920, 1280, 1, 256, 256},
		{"undersized preview is not upscaled", 1440, 960, 1, 1600, 1440},
		{"portrait by orientation 6 uses the height", 1920, 1280, 6, 1600, 1280},
		{"portrait by orientation 8 under the thumb limit", 1920, 1280, 8, 256, 256},
		{"orientation 3 keeps the width", 1920, 1280, 3, 1600, 1600},
		{"unknown orientation is 1", 1500, 1000, 0, 1600, 1500},
		{"orientation 5 swaps", 1000, 1700, 5, 1600, 1600},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := expectedRawWidth(c.w, c.h, c.orient, c.limit); got != c.want {
				t.Fatalf("expectedRawWidth(%d, %d, %d, %d) = %d; want %d", c.w, c.h, c.orient, c.limit, got, c.want)
			}
		})
	}
}

func TestSplitCameras(t *testing.T) {
	cases := []struct {
		name              string
		keys              []string
		wantSplit, bodies int
	}{
		{name: "none", keys: nil},
		{name: "one body", keys: []string{"Acme Model One#SN-0001"}},
		{name: "one serial-less key", keys: []string{"Acme Model One#"}},
		{
			name:      "serial-less key beside one serial: one camera split",
			keys:      []string{"Acme Model One#SN-0001", "Acme Model One#"},
			wantSplit: 1,
		},
		{
			name:   "two bodies of one model",
			keys:   []string{"Acme Model One#SN-0001", "Acme Model One#SN-0002"},
			bodies: 1,
		},
		{
			name:   "two bodies and serial-less files: which body is unknowable",
			keys:   []string{"Acme Model One#SN-0001", "Acme Model One#SN-0002", "Acme Model One#"},
			bodies: 1,
		},
		{
			name:      "one serial under two spellings of the model",
			keys:      []string{"Acme Model One#SN-0001", "ACME  model one#SN-0001"},
			wantSplit: 1,
		},
		{
			name:      "one serial in two cases",
			keys:      []string{"Acme Model One#SN-0001", "Acme Model One#sn-0001"},
			wantSplit: 1,
		},
		{
			name:      "serial-less spelling beside one serial",
			keys:      []string{"Acme Model One#SN-0001", "ACME Model One#"},
			wantSplit: 1,
		},
		{
			name:      "serial-less under two spellings",
			keys:      []string{"Acme Model One#", "ACME Model One#"},
			wantSplit: 1,
		},
		{
			name:   "two bodies, one of them also spelled differently",
			keys:   []string{"Acme Model One#SN-0001", "ACME Model One#SN-0001", "Acme Model One#SN-0002"},
			bodies: 1, wantSplit: 1,
		},
		{
			name: "two models, one split and one with two bodies",
			keys: []string{
				"Acme Model One#SN-0001", "Acme Model One#",
				"Acme Model Two#SN-0003", "Acme Model Two#SN-0004",
			},
			wantSplit: 1, bodies: 1,
		},
		{
			name: "different models are never compared",
			keys: []string{"Acme Model One#SN-0001", "Acme Model Two#", "Other Model One#SN-0001"},
		},
		{
			name: "Unknown and empty keys are ignored",
			keys: []string{"Unknown", "", "Acme Model One#SN-0001"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			split, bodies := splitCameras(c.keys)
			if split != c.wantSplit || bodies != c.bodies {
				t.Fatalf("splitCameras(%q) = %d split, %d with several bodies; want %d, %d",
					c.keys, split, bodies, c.wantSplit, c.bodies)
			}
		})
	}
}

func TestSelectFolders(t *testing.T) {
	cases := []struct {
		name    string
		n, only int
		want    []int
		wantErr bool
	}{
		{name: "every folder", n: 3, only: 0, want: []int{0, 1, 2}},
		{name: "the first", n: 3, only: 1, want: []int{0}},
		{name: "the last", n: 3, only: 3, want: []int{2}},
		{name: "past the end", n: 3, only: 4, wantErr: true},
		{name: "negative", n: 3, only: -1, wantErr: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := selectFolders(c.n, c.only)
			if c.wantErr {
				if err == nil {
					t.Fatalf("selectFolders(%d, %d) = %v; want an error", c.n, c.only, got)
				}
				return
			}
			if err != nil || !slices.Equal(got, c.want) {
				t.Fatalf("selectFolders(%d, %d) = %v, %v; want %v", c.n, c.only, got, err, c.want)
			}
		})
	}
}
