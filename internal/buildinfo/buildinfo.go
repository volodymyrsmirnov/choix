// Package buildinfo holds the version and commit the binary was built from.
// Both are set at link time:
//
//	-ldflags "-X github.com/volodymyrsmirnov/choix/internal/buildinfo.Version=1.2.3
//	          -X github.com/volodymyrsmirnov/choix/internal/buildinfo.Commit=abc1234"
package buildinfo

// Version is the release version; Commit is the source revision. A build
// without -ldflags -X reports the defaults.
var Version, Commit = "0.0.0-dev", "unknown"
