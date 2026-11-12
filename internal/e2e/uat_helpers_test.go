package e2e_test

// Helpers of TestUAT (uat_test.go, build tags uat and darwin) that need no
// corpus: the redaction of everything printed, the report writer, the RAW
// sizing rule, the device-key split rule and the -folder selection. They
// carry no build tag so that their unit tests (uat_helpers_unit_test.go)
// run with every `go test ./...`.

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// --- redaction ----------------------------------------------------------------
//
// Text from outside the test — error messages, a stored error column, a
// tool's output — is redacted by what the test knows, not by what a path
// looks like:
//
//  1. an error that carries a path (*fs.PathError, *os.LinkError,
//     *os.SyscallError) is reduced to its operation and the system error;
//  2. any other text has every known root (corpus, temporary, data, clones,
//     home) cut together with the rest of its path, then every known name —
//     each relative path of the corpus tree, each directory prefix of one,
//     each component of length ≥ 2 with and without its extension, in every
//     spelling an error may carry (raw, %q-escaped, OS separators) —
//     replaced, longest first;
//  3. a final guard withholds the whole text when a known name or a path
//     separator followed by a non-space character is still in it.
//
// Over-redaction is fine; a leaked name is not. The name set lives in
// memory only.

// pathLike matches a path separator followed by a non-space character.
var pathLike = regexp.MustCompile(`[/\\][^\s]`)

// openQuotes are the characters that may open a quoted path, in pairs with
// closeQuotes.
var (
	openQuotes  = []string{`"`, `'`, "`", "“", "‘", "«"}
	closeQuotes = []string{`"`, `'`, "`", "”", "’", "»"}
)

// pathEnd returns where a path that begins at byte i of s ends: at the end
// of the line, or — when a quote opens just before i — at the last matching
// closing quote on that line (a name may hold that quote itself, so the
// last one is taken: that can only cut too much). A space never ends a path.
func pathEnd(s string, i int) int {
	eol := len(s)
	if j := strings.IndexByte(s[i:], '\n'); j >= 0 {
		eol = i + j
	}
	for k, q := range openQuotes {
		if i >= len(q) && s[i-len(q):i] == q {
			if j := strings.LastIndex(s[i:eol], closeQuotes[k]); j >= 0 {
				return i + j
			}
			break
		}
	}
	return eol
}

// redactor redacts text from outside the test. It is safe for concurrent
// use.
type redactor struct {
	mu       sync.Mutex
	roots    []string        // absolute directories, cut with the rest of their path
	names    map[string]bool // every spelling of every known name
	sorted   []string        // names, longest first; nil when stale
	withheld int             // texts the final guard withheld
}

func newRedactor() *redactor { return &redactor{names: map[string]bool{}} }

// addRoots adds absolute directories, and their canonical paths.
func (r *redactor) addRoots(dirs ...string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, d := range dirs {
		d = strings.TrimRight(d, "/")
		if d == "" {
			continue
		}
		r.roots = append(r.roots, d)
		if real, err := filepath.EvalSymlinks(d); err == nil && real != d {
			r.roots = append(r.roots, real)
		}
	}
	sort.Slice(r.roots, func(i, j int) bool { return len(r.roots[i]) > len(r.roots[j]) })
}

// addPaths adds relative, slash-separated paths of the corpus tree: each
// path, each directory prefix of it and each component of length ≥ 2 (with
// and without its extension), in each spelling.
func (r *redactor) addPaths(paths ...string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, p := range paths {
		p = strings.Trim(p, "/")
		if p == "" || p == "." {
			continue
		}
		parts := strings.Split(p, "/")
		for i := range parts {
			r.addName(strings.Join(parts[:i+1], "/")) // prefixes, and p itself
			c := parts[i]
			r.addName(c)
			r.addName(strings.TrimSuffix(c, path.Ext(c)))
		}
	}
	r.sorted = nil
}

// addName adds one name in its spellings: raw, %q-escaped and with the
// OS's separators. Names shorter than two bytes are left out.
func (r *redactor) addName(n string) {
	if len(n) < 2 {
		return
	}
	q := strconv.Quote(n)
	for _, s := range []string{n, q[1 : len(q)-1], filepath.FromSlash(n)} {
		r.names[s] = true
	}
}

// longestFirst returns the names, longest first. Called holding mu.
func (r *redactor) longestFirst() []string {
	if r.sorted == nil {
		r.sorted = make([]string, 0, len(r.names))
		for n := range r.names {
			r.sorted = append(r.sorted, n)
		}
		sort.Slice(r.sorted, func(i, j int) bool {
			if len(r.sorted[i]) != len(r.sorted[j]) {
				return len(r.sorted[i]) > len(r.sorted[j])
			}
			return r.sorted[i] < r.sorted[j]
		})
	}
	return r.sorted
}

// boundedRE matches a two-byte name standing alone (not inside a longer
// word), which is how such a short name is replaced and detected.
func boundedRE(n string) *regexp.Regexp {
	return regexp.MustCompile(`(^|[^\pL\pN])` + regexp.QuoteMeta(n) + `($|[^\pL\pN])`)
}

// text returns s, from outside the test, safe to print: roots cut, known
// names replaced, then the final guard. typ names s's Go type for the
// withheld notice.
func (r *redactor) text(s, typ string) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, root := range r.roots {
		for {
			i := strings.Index(s, root)
			if i < 0 {
				break
			}
			s = s[:i] + "<path>" + s[pathEnd(s, i):]
		}
	}
	names := r.longestFirst()
	for _, n := range names {
		if !strings.Contains(s, n) {
			continue
		}
		if len(n) >= 3 {
			s = strings.ReplaceAll(s, n, "<name>")
		} else {
			s = boundedRE(n).ReplaceAllString(s, "${1}<name>${2}")
		}
	}
	leak := pathLike.MatchString(s)
	for _, n := range names {
		if leak {
			break
		}
		if strings.Contains(s, n) && (len(n) >= 3 || boundedRE(n).MatchString(s)) {
			leak = true
		}
	}
	if leak {
		r.withheld++
		return "<error text withheld> (" + typ + ")"
	}
	return s
}

// errText returns err's text safe to print. An error that carries a path
// (*fs.PathError, *os.LinkError, *os.SyscallError, anywhere in its chain) is
// reduced to its operation and the underlying error's text — the wrapping
// text, which may repeat the path, is dropped; any other error's text goes
// through text.
func (r *redactor) errText(err error) string {
	if err == nil {
		return "<nil>"
	}
	var pe *fs.PathError
	if errors.As(err, &pe) {
		return pe.Op + ": " + r.text(fmt.Sprint(pe.Err), fmt.Sprintf("%T", pe.Err))
	}
	var le *os.LinkError
	if errors.As(err, &le) {
		return le.Op + ": " + r.text(fmt.Sprint(le.Err), fmt.Sprintf("%T", le.Err))
	}
	var se *os.SyscallError
	if errors.As(err, &se) {
		return se.Syscall + ": " + r.text(fmt.Sprint(se.Err), fmt.Sprintf("%T", se.Err))
	}
	return r.text(err.Error(), fmt.Sprintf("%T", err))
}

// withheldCount returns how many texts the final guard withheld.
func (r *redactor) withheldCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.withheld
}

// --- report ------------------------------------------------------------------

// reportDirName and reportName name the report: <tmp>/choix-uat/report.md.
const (
	reportDirName = "choix-uat"
	reportName    = "report.md"
)

// writeReport writes data to <tmpBase>/choix-uat/report.md and returns that
// path. tmpBase is resolved to its canonical path first; the report
// directory must be a real directory there (it is created, 0700, when
// missing) — a symbolic link is refused — and must not lie inside the
// canonical corpus root. The report is written to an exclusively created
// temporary file (0600) in that directory and renamed into place, so an
// existing report path that is a symbolic link is replaced itself and its
// target is never written.
func writeReport(tmpBase, corpusRoot string, data []byte) (string, error) {
	base, err := filepath.EvalSymlinks(tmpBase)
	if err != nil {
		return "", fmt.Errorf("resolve the temporary directory: %w", err)
	}
	dir := filepath.Join(base, reportDirName)
	if corpusRoot != "" {
		root, err := filepath.EvalSymlinks(corpusRoot)
		if err != nil {
			return "", fmt.Errorf("resolve the corpus root: %w", err)
		}
		if rel, err := filepath.Rel(root, dir); err == nil && (rel == "." || filepath.IsLocal(rel)) {
			return "", errors.New("the report directory lies inside the corpus root; refusing to write the report there")
		}
	}
	st, err := os.Lstat(dir)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		if err := os.Mkdir(dir, 0o700); err != nil {
			return "", fmt.Errorf("create the report directory: %w", err)
		}
		if st, err = os.Lstat(dir); err != nil {
			return "", fmt.Errorf("report directory: %w", err)
		}
	case err != nil:
		return "", fmt.Errorf("report directory: %w", err)
	}
	if st.Mode()&fs.ModeSymlink != 0 || !st.IsDir() {
		return "", errors.New("the report directory is not a real directory; refusing to write the report")
	}

	tmp, err := os.CreateTemp(dir, reportName+".*.tmp") // O_CREATE|O_EXCL, 0600
	if err != nil {
		return "", fmt.Errorf("create the report: %w", err)
	}
	tmpName := tmp.Name()
	_, werr := tmp.Write(data)
	if werr == nil {
		werr = tmp.Sync()
	}
	if cerr := tmp.Close(); werr == nil {
		werr = cerr
	}
	if werr == nil {
		werr = os.Rename(tmpName, filepath.Join(dir, reportName))
	}
	if werr != nil {
		_ = os.Remove(tmpName)
		return "", fmt.Errorf("write the report: %w", werr)
	}
	return filepath.Join(dir, reportName), nil
}

// --- the RAW sizing rule -------------------------------------------------------

// expectedRawWidth is the width of a RAW still's cached tier made from its
// embedded JPEG of w×h stored pixels shown with EXIF orientation (1–8;
// anything else is 1): the displayed width — the stored height when the
// orientation swaps the sides (5–8) — bounded by limit and never upscaled.
func expectedRawWidth(w, h, orientation, limit int) int {
	shown := w
	if orientation >= 5 && orientation <= 8 {
		shown = h
	}
	return min(limit, shown)
}

// --- device keys -----------------------------------------------------------------

// cameraModel returns the "<Make> <Model>" part of a device key, lower-case
// with its spaces collapsed, so that two spellings of one model compare
// equal.
func cameraModel(key string) string {
	if i := strings.LastIndex(key, "#"); i >= 0 {
		key = key[:i]
	}
	return strings.Join(strings.Fields(strings.ToLower(key)), " ")
}

// splitCameras looks at the distinct device keys of one folder ("Unknown"
// and "" are ignored) and returns how many camera models (cameraModel) have
// one camera split into more than one device, and how many have more than
// one body.
//
// A model is split when
//   - one serial (compared trimmed and case-folded; "" is the serial-less
//     key) appears under two or more spellings of the key, or
//   - a serial-less key sits beside exactly one serial'd body: the
//     serial-less files can only be that body's.
//
// Two or more distinct serials are that many bodies — the same model owned
// twice — and correct as separate devices; serial-less files beside them
// cannot be told apart, so they are not a split either.
func splitCameras(keys []string) (split, bodies int) {
	// model → serial → the key spellings carrying it
	byModel := map[string]map[string]map[string]bool{}
	for _, k := range keys {
		if k == "" || k == "Unknown" {
			continue
		}
		serial := ""
		if i := strings.LastIndex(k, "#"); i >= 0 {
			serial = strings.ToLower(strings.TrimSpace(k[i+1:]))
		}
		m := cameraModel(k)
		if byModel[m] == nil {
			byModel[m] = map[string]map[string]bool{}
		}
		if byModel[m][serial] == nil {
			byModel[m][serial] = map[string]bool{}
		}
		byModel[m][serial][k] = true
	}
	for _, serials := range byModel {
		spelled := false
		for _, spellings := range serials {
			if len(spellings) > 1 {
				spelled = true
			}
		}
		_, serialLess := serials[""]
		bodyCount := len(serials)
		if serialLess {
			bodyCount--
		}
		if bodyCount > 1 {
			bodies++
		}
		if spelled || serialLess && bodyCount == 1 {
			split++
		}
	}
	return split, bodies
}

// --- folder selection ------------------------------------------------------------

// selectFolders returns the indexes of the n folders (largest first) that a
// run scans: every one for only 0, else folder-only alone (1-based).
func selectFolders(n, only int) ([]int, error) {
	if only < 0 || only > n {
		return nil, fmt.Errorf("-folder %d: the corpus has %d folder(s)", only, n)
	}
	if only > 0 {
		return []int{only - 1}, nil
	}
	out := make([]int, n)
	for i := range out {
		out[i] = i
	}
	return out, nil
}
