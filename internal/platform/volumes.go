package platform

// VolumeInfo describes the volume a path lives on.
type VolumeInfo struct {
	// UUID is the volume's UUID, or "" when the volume has none (network
	// shares).
	UUID string
	// Name is the volume's display name.
	Name      string
	ReadOnly  bool
	Removable bool
}

// Volumes answers questions about mounted volumes and the Trash.
type Volumes interface {
	// Info returns the volume path lives on.
	Info(path string) (VolumeInfo, error)
	// Mounted reports whether the volume with this UUID is mounted.
	Mounted(uuid string) bool
	// InTrash reports true only for a verified relationship to the user's
	// or the item's volume's Trash. It returns an error when that cannot be
	// determined; the caller retries under an access lease.
	InTrash(path string) (bool, error)
	// AbsentOn reports true only when lstat(path) fails with ENOENT ("no
	// such file", never "not permitted") and the nearest existing ancestor
	// of path lies on the mounted volume whose UUID is uuid — so the probe
	// was made on the volume the folder lived on, not at a stale mount
	// point.
	AbsentOn(path, uuid string) bool
}
