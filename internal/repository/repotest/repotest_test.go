package repotest

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMain(m *testing.M) {
	os.Exit(Run(m))
}

func TestPoolUsesIsolatedMigratedSchema(t *testing.T) {
	p := Pool(t)
	var gotSchema string
	require.NoError(t, p.QueryRow(context.Background(), `SELECT current_schema()`).Scan(&gotSchema))
	assert.True(t, strings.HasPrefix(gotSchema, "couchcast_test_"), gotSchema)
	assert.NotEqual(t, "public", gotSchema)

	var usersTable string
	require.NoError(t, p.QueryRow(context.Background(), `SELECT to_regclass('users')::text`).Scan(&usersTable))
	assert.Equal(t, "users", usersTable)
}

func TestDatabaseURLTargetsIsolatedSchema(t *testing.T) {
	Pool(t)
	u := DatabaseURL(t)
	assert.Contains(t, u, "search_path=couchcast_test_")

	p, err := pgxpool.New(t.Context(), u)
	require.NoError(t, err)
	t.Cleanup(p.Close)
	var gotSchema, applicationName string
	require.NoError(t, p.QueryRow(t.Context(), `SELECT current_schema(), current_setting('application_name')`).Scan(&gotSchema, &applicationName))
	assert.Equal(t, schema, gotSchema)
	assert.Equal(t, schema, applicationName)
}

func TestSandboxCloseDropsOnlyItsSchema(t *testing.T) {
	p := Pool(t)
	extra, err := setup(baseURL)
	require.NoError(t, err)
	closed := false
	t.Cleanup(func() {
		if !closed {
			_ = extra.close()
		}
	})
	assert.NotEqual(t, schema, extra.schema)

	var exists bool
	require.NoError(t, p.QueryRow(t.Context(), `SELECT EXISTS (SELECT 1 FROM pg_namespace WHERE nspname = $1)`, extra.schema).Scan(&exists))
	require.True(t, exists)

	require.NoError(t, extra.close())
	closed = true
	require.NoError(t, p.QueryRow(t.Context(), `SELECT EXISTS (SELECT 1 FROM pg_namespace WHERE nspname = $1)`, extra.schema).Scan(&exists))
	assert.False(t, exists)
	require.NoError(t, p.QueryRow(t.Context(), `SELECT EXISTS (SELECT 1 FROM pg_namespace WHERE nspname = $1)`, schema).Scan(&exists))
	assert.True(t, exists)
}

func TestWithSandboxParams(t *testing.T) {
	got, err := withSandboxParams("postgres://user:pass@db.example/couchcast?sslmode=disable", "couchcast_test_1234")
	require.NoError(t, err)
	assert.Contains(t, got, "search_path=couchcast_test_1234")
	assert.Contains(t, got, "application_name=couchcast_test_1234")
	assert.Contains(t, got, "sslmode=disable")

	got, err = withSandboxParams("host=db.example dbname=couchcast", "couchcast_test_1234")
	require.NoError(t, err)
	assert.Equal(t, "host=db.example dbname=couchcast search_path=couchcast_test_1234 application_name=couchcast_test_1234", got)
}
