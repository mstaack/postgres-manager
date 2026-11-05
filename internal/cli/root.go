// Package cli defines the pgm command tree.
package cli

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"
)

func newRoot(version string) *cobra.Command {
	root := &cobra.Command{
		Use:           "pgm",
		Short:         "PostgreSQL manager for Debian/Ubuntu servers: status, databases, users (incl. read-only), extensions and pgBackRest point-in-time recovery.",
		Version:       version,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.SetVersionTemplate("{{.Version}}\n")
	return root
}

// Execute runs the pgm command tree. It returns when the command finishes or
// when SIGINT/SIGTERM cancels the context.
func Execute(version string) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return newRoot(version).ExecuteContext(ctx)
}
