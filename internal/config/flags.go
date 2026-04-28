package config

import "github.com/spf13/cobra"

// Flag names are package-level constants so commands and tests refer to the
// same strings.
const (
	flagBucketSize = "bucket-size"
	flagPicksDir   = "picks-dir"
)

// Flags registers choix's config flags on the given command.
func Flags(cmd *cobra.Command) {
	f := cmd.PersistentFlags()
	f.Int(flagBucketSize, 0, "time bucket size in seconds (default 600)")
	f.String(flagPicksDir, "", "directory under scan root for exported picks (default \"picks\")")
}

// ApplyFlags copies flag values into cfg, but only for flags the user actually
// set on the command line.
func ApplyFlags(cfg *Config, cmd *cobra.Command) error {
	f := cmd.Flags()

	if f.Changed(flagBucketSize) {
		v, err := f.GetInt(flagBucketSize)
		if err != nil {
			return err
		}
		cfg.BucketSizeSec = v
	}
	if f.Changed(flagPicksDir) {
		v, err := f.GetString(flagPicksDir)
		if err != nil {
			return err
		}
		cfg.PicksDir = v
	}
	return nil
}
