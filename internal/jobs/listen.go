package jobs

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Listen holds a dedicated connection on the channel and calls fn for every
// notification until ctx is cancelled. Connection loss is retried with a
// short delay; callers that need at-least-once semantics must also poll.
func Listen(ctx context.Context, pool *pgxpool.Pool, channel string, logger *slog.Logger, fn func(payload string)) error {
	for {
		err := listenOnce(ctx, pool, channel, fn)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		logger.Warn("listen connection lost, reconnecting", "channel", channel, "error", err)

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
}

func listenOnce(ctx context.Context, pool *pgxpool.Pool, channel string, fn func(string)) error {
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return err
	}
	// The connection has been used for LISTEN; hijack it so the pool never
	// hands it to anyone else, and close it when we are done.
	raw := conn.Hijack()
	defer func() { _ = raw.Close(context.Background()) }()

	if _, err := raw.Exec(ctx, "LISTEN "+sanitizeIdent(channel)); err != nil {
		return err
	}

	for {
		n, err := raw.WaitForNotification(ctx)
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return ctx.Err()
			}
			return err
		}
		fn(n.Payload)
	}
}

// sanitizeIdent quotes a channel name as an identifier.
func sanitizeIdent(s string) string {
	out := make([]byte, 0, len(s)+2)
	out = append(out, '"')
	for i := range len(s) {
		if s[i] == '"' {
			out = append(out, '"')
		}
		out = append(out, s[i])
	}
	return string(append(out, '"'))
}
