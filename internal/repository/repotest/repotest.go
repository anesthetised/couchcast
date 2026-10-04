// Package repotest gives each integration-test binary an isolated PostgreSQL
// schema with the application migrations applied. Tests skip when
// COUCHCAST_TEST_DATABASE_URL is unset, so the unit suite still runs without a
// database.
package repotest

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	setupOnce   sync.Once
	runCalled   bool
	baseURL     string
	sandboxTest *sandbox
	errSetup    error
	pool        *pgxpool.Pool
	databaseURL string
	schema      string
)

// Run creates one isolated schema for a test binary, runs its tests, then drops
// the schema. Every package that calls Pool must call Run from TestMain.
func Run(m *testing.M) int {
	runCalled = true
	baseURL = os.Getenv("COUCHCAST_TEST_DATABASE_URL")

	code := m.Run()
	if sandboxTest == nil {
		return code
	}
	if err := sandboxTest.close(); err != nil {
		fmt.Fprintf(os.Stderr, "repotest: cleanup: %v\n", err)
		if code == 0 {
			code = 1
		}
	}
	pool = nil
	return code
}

// Pool returns the package's pool after truncating its isolated schema, or
// skips when integration tests are disabled.
func Pool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	ensureSetup(t)

	if err := truncate(context.Background(), pool, schema); err != nil {
		t.Fatalf("repotest: truncate: %v", err)
	}

	return pool
}

// DatabaseURL returns the isolated connection string for commands that open
// their own pool. Pool must be called first so the test starts from clean data.
func DatabaseURL(t *testing.T) string {
	t.Helper()
	ensureSetup(t)
	return databaseURL
}

func ensureSetup(t *testing.T) {
	t.Helper()
	if !runCalled {
		t.Fatal("repotest: package TestMain must call repotest.Run")
	}
	if baseURL == "" {
		t.Skip("COUCHCAST_TEST_DATABASE_URL not set")
	}
	setupOnce.Do(func() {
		sandboxTest, errSetup = setup(baseURL)
		if errSetup == nil {
			pool, databaseURL, schema = sandboxTest.pool, sandboxTest.url, sandboxTest.schema
		}
	})
	if errSetup != nil {
		t.Fatalf("repotest: setup: %v", errSetup)
	}
}

type sandbox struct {
	admin  *pgxpool.Pool
	pool   *pgxpool.Pool
	schema string
	url    string
}

func setup(baseURL string) (*sandbox, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	admin, err := pgxpool.New(ctx, baseURL)
	if err != nil {
		return nil, err
	}

	schema, err := schemaName()
	if err != nil {
		admin.Close()
		return nil, err
	}
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+pgx.Identifier{schema}.Sanitize()); err != nil {
		admin.Close()
		return nil, err
	}

	isolatedURL, err := withSandboxParams(baseURL, schema)
	if err != nil {
		_, _ = admin.Exec(ctx, "DROP SCHEMA "+pgx.Identifier{schema}.Sanitize()+" CASCADE")
		admin.Close()
		return nil, err
	}
	config, err := pgxpool.ParseConfig(isolatedURL)
	if err != nil {
		_, _ = admin.Exec(ctx, "DROP SCHEMA "+pgx.Identifier{schema}.Sanitize()+" CASCADE")
		admin.Close()
		return nil, err
	}
	p, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		_, _ = admin.Exec(ctx, "DROP SCHEMA "+pgx.Identifier{schema}.Sanitize()+" CASCADE")
		admin.Close()
		return nil, err
	}
	if err := applyMigrations(ctx, p, schema); err != nil {
		p.Close()
		_, _ = admin.Exec(ctx, "DROP SCHEMA "+pgx.Identifier{schema}.Sanitize()+" CASCADE")
		admin.Close()
		return nil, err
	}
	return &sandbox{admin: admin, pool: p, schema: schema, url: isolatedURL}, nil
}

func (s *sandbox) close() error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	s.pool.Close()
	defer s.admin.Close()
	_, err := s.admin.Exec(ctx, "DROP SCHEMA "+pgx.Identifier{s.schema}.Sanitize()+" CASCADE")
	return err
}

func schemaName() (string, error) {
	var id [8]byte
	if _, err := rand.Read(id[:]); err != nil {
		return "", err
	}
	return "couchcast_test_" + hex.EncodeToString(id[:]), nil
}

func withSandboxParams(baseURL, schema string) (string, error) {
	if strings.HasPrefix(baseURL, "postgres://") || strings.HasPrefix(baseURL, "postgresql://") {
		u, err := url.Parse(baseURL)
		if err != nil {
			return "", err
		}
		q := u.Query()
		q.Set("search_path", schema)
		q.Set("application_name", schema)
		u.RawQuery = q.Encode()
		return u.String(), nil
	}
	return strings.TrimSpace(baseURL) + " search_path=" + schema + " application_name=" + schema, nil
}

// applyMigrations replays the committed migrations into the isolated schema.
// Atlas qualifies objects as "public", so the test runner substitutes only
// that quoted identifier; values such as the room visibility 'public' remain
// untouched.
func applyMigrations(ctx context.Context, p *pgxpool.Pool, schema string) error {

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
		contents, err := os.ReadFile(filepath.Join(dir, name)) //nolint:gosec // path is our own migrations dir
		if err != nil {
			return err
		}
		sql := strings.ReplaceAll(string(contents), `"public"`, pgx.Identifier{schema}.Sanitize())
		if _, err := p.Exec(ctx, sql); err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
	}

	return nil
}

func truncate(ctx context.Context, p *pgxpool.Pool, schema string) error {
	rows, err := p.Query(ctx, `SELECT tablename FROM pg_catalog.pg_tables WHERE schemaname = $1 ORDER BY tablename`, schema)
	if err != nil {
		return err
	}
	defer rows.Close()

	var tables []string
	for rows.Next() {
		var table string
		if err := rows.Scan(&table); err != nil {
			return err
		}
		tables = append(tables, pgx.Identifier{schema, table}.Sanitize())
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if len(tables) == 0 {
		return nil
	}
	_, err = p.Exec(ctx, "TRUNCATE "+strings.Join(tables, ", ")+" RESTART IDENTITY CASCADE")
	return err
}

// migrationsDir locates db/migrations relative to this source file so the
// helper works from any package.
func migrationsDir() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(file), "..", "..", "..", "db", "migrations")
}
