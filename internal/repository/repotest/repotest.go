// Package repotest gives integration tests a PostgreSQL pool with the
// application schema applied. Tests skip when COUCHCAST_TEST_DATABASE_URL
// is unset, so the unit suite still runs without a database.
package repotest

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	once     sync.Once
	pool     *pgxpool.Pool
	errSetup error
)

// Pool returns a pool against a freshly truncated schema, or skips the test.
func Pool(t *testing.T) *pgxpool.Pool {
	t.Helper()

	url := os.Getenv("COUCHCAST_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("COUCHCAST_TEST_DATABASE_URL not set")
	}

	once.Do(func() { pool, errSetup = setup(url) })
	if errSetup != nil {
		t.Fatalf("repotest: %v", errSetup)
	}

	_, err := pool.Exec(context.Background(), `
		TRUNCATE users, sessions, media, media_blocklist, media_reports, rooms,
		         room_members, room_bans, invites, queue_items, queue_votes,
		         messages, jobs, audit_log
		RESTART IDENTITY CASCADE
	`)
	if err != nil {
		t.Fatalf("repotest: truncate: %v", err)
	}

	return pool
}

func setup(url string) (*pgxpool.Pool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	p, err := pgxpool.New(ctx, url)
	if err != nil {
		return nil, err
	}
	if err := resetSchema(ctx, p); err != nil {
		p.Close()
		return nil, err
	}
	return p, nil
}

// resetSchema drops everything and replays db/migrations so tests run
// against exactly the schema the application sees.
func resetSchema(ctx context.Context, p *pgxpool.Pool) error {
	if _, err := p.Exec(ctx, `DROP SCHEMA public CASCADE; CREATE SCHEMA public;`); err != nil {
		return err
	}

	dir := migrationsDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}

	var files []string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".sql") {
			files = append(files, e.Name())
		}
	}
	sort.Strings(files)

	for _, name := range files {
		sql, err := os.ReadFile(filepath.Join(dir, name)) //nolint:gosec // path is our own migrations dir
		if err != nil {
			return err
		}
		if _, err := p.Exec(ctx, string(sql)); err != nil {
			return err
		}
	}

	return nil
}

// migrationsDir locates db/migrations relative to this source file so the
// helper works from any package.
func migrationsDir() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(file), "..", "..", "..", "db", "migrations")
}
