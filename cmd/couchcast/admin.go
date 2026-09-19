package main

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/anesthetised/couchcast/internal/config"
)

// admin dispatches administrative subcommands. Only the command shape exists
// until the user model lands in phase 2 and the admin role in phase 8.
func admin(_ context.Context, _ config.Config, _ *slog.Logger, args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("admin: missing subcommand (expected: grant <username>)")
	}

	switch args[0] {
	case "grant":
		if len(args) != 2 {
			return fmt.Errorf("admin grant: expected exactly one username")
		}
		return fmt.Errorf("admin grant: not implemented yet")
	default:
		return fmt.Errorf("admin: unknown subcommand %q", args[0])
	}
}
