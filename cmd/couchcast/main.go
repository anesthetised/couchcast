// Command couchcast is the single binary behind the service. It runs as the
// web server (serve), the ingest worker (ingest) or an administrative CLI
// (admin), selected by the first argument.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/anesthetised/couchcast/internal/config"
)

// version is set at build time via -ldflags "-X main.version=...".
var version = "dev"

const usage = `usage: couchcast <command> [args]

commands:
  serve                  run the web server (default)
  ingest                 run the ingest worker
  admin grant <username> grant site administrator role to a user
  version                print the build version
`

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)

	err := run(ctx, os.Args[1:])
	stop()

	if err != nil && !errors.Is(err, context.Canceled) {
		fmt.Fprintln(os.Stderr, "couchcast:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string) error {
	cmd := "serve"
	if len(args) > 0 {
		cmd, args = args[0], args[1:]
	}

	cfg := config.Load()
	logger := newLogger(cfg.LogLevel).With("version", version)
	slog.SetDefault(logger)

	switch cmd {
	case "serve":
		return serve(ctx, cfg, logger)
	case "ingest":
		return ingest(ctx, cfg, logger)
	case "admin":
		return admin(ctx, cfg, logger, args)
	case "version", "-v", "--version":
		fmt.Println("couchcast", version)
		return nil
	case "help", "-h", "--help":
		fmt.Print(usage)
		return nil
	default:
		fmt.Fprint(os.Stderr, usage)
		return fmt.Errorf("unknown command %q", cmd)
	}
}

func newLogger(level string) *slog.Logger {
	var lvl slog.Level
	if err := lvl.UnmarshalText([]byte(strings.ToUpper(level))); err != nil {
		lvl = slog.LevelInfo
	}

	return slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: lvl}))
}
