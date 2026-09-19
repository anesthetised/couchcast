package repository

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
)

// testPool is shared by every test in the package. It is nil when
// COUCHCAST_TEST_DATABASE_URL is unset, in which case tests skip.
var testPool *pgxpool.Pool

func TestMain(m *testing.M) {
	url := os.Getenv("COUCHCAST_TEST_DATABASE_URL")
	if url == "" {
		os.Exit(m.Run())
	}

	pool, err := setupPool(url)
	if err != nil {
		panic(err)
	}
	testPool = pool

	code := m.Run()
	pool.Close()
	os.Exit(code)
}

func setupPool(url string) (*pgxpool.Pool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		return nil, err
	}
	if err := resetSchema(ctx, pool); err != nil {
		pool.Close()
		return nil, err
	}
	return pool, nil
}

// resetSchema drops everything and replays db/migrations so tests always
// run against the exact schema the application would see.
func resetSchema(ctx context.Context, pool *pgxpool.Pool) error {
	if _, err := pool.Exec(ctx, `DROP SCHEMA public CASCADE; CREATE SCHEMA public;`); err != nil {
		return err
	}

	dir := filepath.Join("..", "..", "db", "migrations")
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
		sql, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return err
		}
		if _, err := pool.Exec(ctx, string(sql)); err != nil {
			return err
		}
	}

	return nil
}

// newTestRepo skips when no database is configured and truncates all
// tables so each test starts clean.
func newTestRepo(t *testing.T) *Repo {
	t.Helper()
	if testPool == nil {
		t.Skip("COUCHCAST_TEST_DATABASE_URL not set")
	}

	_, err := testPool.Exec(context.Background(), `
		TRUNCATE users, sessions, media, media_blocklist, media_reports, rooms,
		         room_members, room_bans, invites, queue_items, queue_votes,
		         messages, jobs, audit_log
		RESTART IDENTITY CASCADE
	`)
	require.NoError(t, err)

	return New(testPool)
}
