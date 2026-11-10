// Package cmd implements the libsql-rest command line.
package cmd

import (
	"runtime/debug"

	"github.com/spf13/cobra"
)

func Execute() error {
	return newRootCmd().Execute()
}

func newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:           "libsql-rest",
		Short:         "Serve the tables of a libSQL or SQLite database as a REST API",
		Version:       version(),
		SilenceErrors: true,
		SilenceUsage:  true,
	}
	root.AddCommand(newServeCmd(), newTokenCmd(), newKeygenCmd())
	return root
}

// go build stamps the main module version from the git tag, so a build of the
// v1.0.0 tag reports v1.0.0.
func version() string {
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" {
		return info.Main.Version
	}
	return "(devel)"
}
