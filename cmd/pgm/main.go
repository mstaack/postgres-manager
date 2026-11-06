package main

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/mstaack/postgres-manager/internal/cli"
	"github.com/mstaack/postgres-manager/internal/ui"
)

var version = "dev"

func main() {
	err := cli.Execute(version)
	switch {
	case err == nil:
	case errors.Is(err, ui.ErrCancelled), errors.Is(err, context.Canceled):
		fmt.Println("↩ cancelled")
		os.Exit(130)
	default:
		fmt.Fprintf(os.Stderr, "✗ %s\n", err)
		os.Exit(1)
	}
}
