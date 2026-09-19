package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/anesthetised/couchcast/internal/config"
	"github.com/anesthetised/couchcast/internal/entity"
	"github.com/anesthetised/couchcast/internal/repository"
)

// admin dispatches administrative subcommands run from the shell.
func admin(ctx context.Context, cfg config.Config, logger *slog.Logger, args []string) error {
	if len(args) < 1 {
		return errors.New("admin: missing subcommand (expected: grant <username> | revoke <username>)")
	}
	if len(args) != 2 {
		return fmt.Errorf("admin %s: expected exactly one username", args[0])
	}

	var role entity.Role
	switch args[0] {
	case "grant":
		role = entity.RoleAdmin
	case "revoke":
		role = entity.RoleUser
	default:
		return fmt.Errorf("admin: unknown subcommand %q", args[0])
	}

	if err := cfg.Database.Validate(); err != nil {
		return fmt.Errorf("config: %w", err)
	}
	pool, err := connectDB(ctx, cfg.Database, logger)
	if err != nil {
		return err
	}
	defer pool.Close()
	repo := repository.New(pool)

	username := strings.TrimSpace(args[1])
	user, err := repo.GetUserByUsername(ctx, username)
	if err != nil {
		return fmt.Errorf("user %q: %w", username, err)
	}
	if err := repo.SetUserRole(ctx, user.ID, role); err != nil {
		return err
	}
	fmt.Printf("user %s is now %s\n", user.Username, role)
	return nil
}
