package platformtest

import (
	"errors"
	"fmt"
	"sync"

	"github.com/volodymyrsmirnov/choix/internal/platform"
)

// volume is one volume added with Add.
type volume struct {
	prefix  string
	info    platform.VolumeInfo
	mounted bool
}

// trashRule is what InTrash answers for a path at or under prefix.
type trashRule struct {
	prefix string
	kind   trashKind
	access *Bookmarks
}

type trashKind int

const (
	trashed trashKind = iota
	trashUnknown
	trashNeedsAccess
)

// absentKey identifies one SetAbsent answer.
type absentKey struct{ path, uuid string }

// Volumes is an in-memory platform.Volumes. Paths belong to the mounted
// volume added with the longest matching prefix; a volume that is unmounted
// no longer claims its prefix. The zero value is ready to use; it is safe
// for concurrent use.
type Volumes struct {
	mu     sync.Mutex
	vols   []*volume
	trash  []trashRule
	absent map[absentKey]bool
}

var _ platform.Volumes = (*Volumes)(nil)

// Add registers a mounted volume claiming every path at or under prefix.
func (v *Volumes) Add(prefix string, info platform.VolumeInfo) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.vols = append(v.vols, &volume{prefix: prefix, info: info, mounted: true})
}

// setMounted changes the mounted state of every volume with uuid.
func (v *Volumes) setMounted(uuid string, mounted bool) {
	v.mu.Lock()
	defer v.mu.Unlock()
	for _, vol := range v.vols {
		if vol.info.UUID == uuid {
			vol.mounted = mounted
		}
	}
}

// Unmount marks the volume with uuid unmounted.
func (v *Volumes) Unmount(uuid string) { v.setMounted(uuid, false) }

// Mount marks the volume with uuid mounted again.
func (v *Volumes) Mount(uuid string) { v.setMounted(uuid, true) }

// Info returns the mounted volume with the longest prefix matching path.
func (v *Volumes) Info(path string) (platform.VolumeInfo, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	var best *volume
	for _, vol := range v.vols {
		if vol.mounted && under(path, vol.prefix) && (best == nil || len(vol.prefix) > len(best.prefix)) {
			best = vol
		}
	}
	if best == nil {
		return platform.VolumeInfo{}, fmt.Errorf("platformtest: volume info %s: no mounted volume", path)
	}
	return best.info, nil
}

// Mounted reports whether a volume with this non-empty uuid is mounted.
func (v *Volumes) Mounted(uuid string) bool {
	if uuid == "" {
		return false
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	for _, vol := range v.vols {
		if vol.info.UUID == uuid && vol.mounted {
			return true
		}
	}
	return false
}

func (v *Volumes) addTrash(r trashRule) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.trash = append(v.trash, r)
}

// Trash puts path, and everything inside it, in the Trash.
func (v *Volumes) Trash(path string) { v.addTrash(trashRule{prefix: path, kind: trashed}) }

// TrashUnknown makes InTrash fail for path and everything inside it.
func (v *Volumes) TrashUnknown(path string) { v.addTrash(trashRule{prefix: path, kind: trashUnknown}) }

// TrashNeedsAccess makes InTrash fail for path and everything inside it
// unless b holds an active access lease covering the path; under such a
// lease InTrash answers as it otherwise would.
func (v *Volumes) TrashNeedsAccess(path string, b *Bookmarks) {
	v.addTrash(trashRule{prefix: path, kind: trashNeedsAccess, access: b})
}

// InTrash answers from the rules set with Trash, TrashUnknown and
// TrashNeedsAccess; a path no rule covers is not in the Trash.
func (v *Volumes) InTrash(path string) (bool, error) {
	v.mu.Lock()
	rules := append([]trashRule(nil), v.trash...)
	v.mu.Unlock()
	in := false
	for _, r := range rules {
		if !under(path, r.prefix) {
			continue
		}
		switch r.kind {
		case trashUnknown:
			return false, fmt.Errorf("platformtest: trash check %s: %w", path, errTrashUndetermined)
		case trashNeedsAccess:
			if !r.access.accessible(path) {
				return false, fmt.Errorf("platformtest: trash check %s: not permitted: %w", path, errTrashUndetermined)
			}
		case trashed:
			in = true
		}
	}
	return in, nil
}

// errTrashUndetermined is the cause of every InTrash failure.
var errTrashUndetermined = errors.New("cannot determine whether the item is in the Trash")

// SetAbsent sets what AbsentOn(path, uuid) answers; it is false until set.
func (v *Volumes) SetAbsent(path, uuid string, absent bool) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.absent == nil {
		v.absent = map[absentKey]bool{}
	}
	v.absent[absentKey{path, uuid}] = absent
}

// AbsentOn returns the value set with SetAbsent for exactly this path and
// uuid, or false.
func (v *Volumes) AbsentOn(path, uuid string) bool {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.absent[absentKey{path, uuid}]
}
