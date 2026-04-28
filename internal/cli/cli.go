// Package cli is the entry point for the choix binary. It exposes a single
// command — the default `choix [folder]` action that scans and serves the
// web UI. The cmd/choix binary delegates to Execute; tests can call Execute
// directly without os.Exit.
package cli

import (
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"
)

// Version is stamped at build time via -ldflags.
var Version = "0.0.0-dev"

// rootCmd constructs the root cobra.Command. The web UI is the only
// supported interface; there are no subcommands.
func rootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:           "choix [folder]",
		Short:         "Photo and video culling tool",
		Version:       Version,
		SilenceUsage:  true,
		SilenceErrors: true,
		Args:          cobra.MaximumNArgs(1),
		RunE:          runDefault,
	}
	addServeFlags(root)
	return root
}

// Execute runs the CLI with the supplied arguments (typically os.Args[1:]).
// Returns 0 on success, 1 on error. Errors are printed to stderr.
func Execute(args []string) int {
	return ExecuteWith(args, os.Stderr)
}

// ExecuteWith is the testable form of Execute, taking an explicit error writer.
func ExecuteWith(args []string, errOut io.Writer) int {
	cmd := rootCmd()
	cmd.SetArgs(args)
	cmd.SetErr(errOut)
	if err := cmd.Execute(); err != nil {
		_, _ = fmt.Fprintf(errOut, "choix: %v\n", err)
		return 1
	}
	return 0
}
