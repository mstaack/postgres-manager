// Package ui holds shared terminal interaction helpers.
package ui

import "errors"

// ErrCancelled is returned when the user cancels an interactive prompt (ESC).
var ErrCancelled = errors.New("cancelled")
