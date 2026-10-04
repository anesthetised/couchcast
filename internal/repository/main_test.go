package repository

import (
	"os"
	"testing"

	"github.com/anesthetised/couchcast/internal/repository/repotest"
)

func TestMain(m *testing.M) {
	os.Exit(repotest.Run(m))
}

// newTestRepo returns a repository over a clean test database, or skips.
func newTestRepo(t *testing.T) *Repo {
	t.Helper()
	return New(repotest.Pool(t))
}
