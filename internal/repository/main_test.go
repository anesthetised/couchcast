package repository

import (
	"testing"

	"github.com/anesthetised/couchcast/internal/repository/repotest"
)

// newTestRepo returns a repository over a clean test database, or skips.
func newTestRepo(t *testing.T) *Repo {
	t.Helper()
	return New(repotest.Pool(t))
}
