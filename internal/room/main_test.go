package room

import (
	"os"
	"testing"

	"github.com/anesthetised/couchcast/internal/repository/repotest"
)

func TestMain(m *testing.M) {
	os.Exit(repotest.Run(m))
}
