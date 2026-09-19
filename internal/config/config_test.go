package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadDefaults(t *testing.T) {
	cfg := Load()

	assert.Equal(t, ":8080", cfg.Web.Addr)
	assert.Equal(t, []int{1080, 720, 480, 360}, cfg.Ingest.QualityLadder)
	assert.Equal(t, 2, cfg.Ingest.Workers)
}

func TestValidate(t *testing.T) {
	t.Setenv("COUCHCAST_DATABASE_URL", "postgres://x")
	t.Setenv("COUCHCAST_S3_ENDPOINT", "minio:9000")
	t.Setenv("COUCHCAST_S3_ACCESS_KEY", "a")
	t.Setenv("COUCHCAST_S3_SECRET_KEY", "b")
	t.Setenv("COUCHCAST_MEDIA_TOKEN_SECRET", "0123456789abcdef0123456789abcdef")
	t.Setenv("COUCHCAST_QUALITY_LADDER", "720, 480,oops")

	cfg := Load()
	require.NoError(t, cfg.Validate())
	assert.Equal(t, []int{720, 480}, cfg.Ingest.QualityLadder)

	t.Setenv("COUCHCAST_MEDIA_TOKEN_SECRET", "short")
	assert.Error(t, Load().Web.Validate())
	assert.NoError(t, Load().Database.Validate())
}
