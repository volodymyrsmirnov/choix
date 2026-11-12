package picks

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/volodymyrsmirnov/choix/internal/safefs"
	"github.com/volodymyrsmirnov/choix/internal/store"
)

// sweepMaxDepth bounds how deep below the picks directory SweepTemps looks.
// Exports mirror the folder's own subdirectories, so real trees are far
// shallower; the bound only stops a pathological one.
const sweepMaxDepth = 64

// SweepTemps removes what interrupted exports left in the picks directory
// dir of the folder root (relative, as Session.PicksDir returns it) and in
// its subdirectories, and returns how many files it removed.
//
// A file is removed only when it is provably Choix's: a regular file named
// as safefs.CreateTemp names its temporaries (safefs.TempBase) whose base is
// a name an export writes into that very directory — the base name of a
// file in st's files table whose own directory the subdirectory mirrors
// (export's layout, <dir>/<dir of the file>/<base>), or that name with a
// collision suffix (_2, _3, …), or the name of an export path recorded in
// st's picks table. Anything else that merely has the temporary form is the
// user's and is kept, as is everything outside dir.
//
// Every directory is opened through safefs, so neither a symlinked
// directory (dir itself is then refused with safefs.ErrSymlink) nor a
// symlinked temporary is followed. A missing dir is 0, nil; nothing is
// created. Errors in subdirectories and lookups are joined, and the sweep
// goes on past them; a file whose ownership could not be established is
// kept.
func SweepTemps(st *store.Store, root, dir string) (int, error) {
	sw := &sweeper{st: st, root: root, dir: cleanDir(dir)}
	n, err := sw.in(sw.dir, 0)
	err = errors.Join(err, errors.Join(sw.errs...))
	if errors.Is(err, fs.ErrNotExist) && n == 0 && len(sw.errs) == 0 {
		return 0, nil
	}
	return n, err
}

// sweeper is one SweepTemps run.
type sweeper struct {
	st        *store.Store
	root, dir string
	// exported holds the recorded export paths, lower-cased, loaded on the
	// first temporary that needs them.
	exported map[string]bool
	errs     []error
}

// in sweeps rel and, down to sweepMaxDepth, its real subdirectories.
func (sw *sweeper) in(rel string, depth int) (int, error) {
	d, err := safefs.OpenDir(sw.root, rel, false)
	if err != nil {
		return 0, fmt.Errorf("picks: sweep %s: %w", rel, err)
	}
	removed, err := d.RemoveOwnTemps(func(base string) bool { return sw.owned(rel, base) })
	errs := []error{err}
	if cerr := d.Close(); cerr != nil {
		errs = append(errs, fmt.Errorf("picks: sweep %s: close: %w", rel, cerr))
	}
	if depth >= sweepMaxDepth {
		return removed, errors.Join(errs...)
	}
	// Listing is a read; each subdirectory is opened again through safefs
	// from root, which refuses it if any component is a symlink.
	entries, err := os.ReadDir(filepath.Join(sw.root, filepath.FromSlash(rel)))
	if err != nil {
		errs = append(errs, fmt.Errorf("picks: sweep %s: list: %w", rel, err))
	}
	for _, e := range entries {
		if !e.IsDir() { // a symlink is not a directory entry here
			continue
		}
		n, err := sw.in(rel+"/"+e.Name(), depth+1)
		removed += n
		if err != nil && !errors.Is(err, fs.ErrNotExist) { // gone meanwhile
			errs = append(errs, err)
		}
	}
	return removed, errors.Join(errs...)
}

// owned reports whether an export writes a file named base into rel, a
// directory of the picks tree.
func (sw *sweeper) owned(rel, base string) bool {
	sub := strings.TrimPrefix(strings.TrimPrefix(rel, sw.dir), "/")
	for _, name := range exportSources(base) {
		p := name
		if sub != "" {
			p = sub + "/" + name
		}
		_, err := sw.st.Files().GetByPath(p)
		switch {
		case err == nil:
			return true
		case !errors.Is(err, store.ErrNotFound):
			sw.errs = append(sw.errs, fmt.Errorf("picks: sweep %s: look up %s: %w", rel, p, err))
			return false
		}
	}
	if sw.exported == nil {
		picked, err := sw.st.Picks().ListPicked()
		if err != nil {
			sw.errs = append(sw.errs, fmt.Errorf("picks: sweep %s: export paths: %w", rel, err))
			return false
		}
		sw.exported = make(map[string]bool, len(picked))
		for _, p := range picked {
			if p.ExportedPath.Valid {
				sw.exported[strings.ToLower(strings.TrimLeft(p.ExportedPath.String, "/"))] = true
			}
		}
	}
	return sw.exported[strings.ToLower(rel+"/"+base)]
}

// exportSources returns the original base names an export could have
// written as name: name itself and, when name is suffixedName(orig, n) for
// some n ≥ 2, orig.
func exportSources(name string) []string {
	out := []string{name}
	ext := path.Ext(name)
	stem := strings.TrimSuffix(name, ext)
	i := strings.LastIndexByte(stem, '_')
	if i < 0 {
		return out
	}
	digits := stem[i+1:]
	if n, err := strconv.Atoi(digits); err == nil && n >= 2 && strconv.Itoa(n) == digits {
		if orig := stem[:i] + ext; suffixedName(orig, n) == name {
			out = append(out, orig)
		}
	}
	return out
}
