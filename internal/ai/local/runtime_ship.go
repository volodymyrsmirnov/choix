//go:build production

package local

// devRuntimeCandidates is empty in the shipping build: the app loads only
// the ONNX Runtime library bundled in Contents/Frameworks, never one named
// by the environment or installed by Homebrew.
func devRuntimeCandidates() []string { return nil }
