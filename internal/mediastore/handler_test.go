package mediastore

import (
	"testing"

	"uuid"

	"github.com/stretchr/testify/assert"
)

func TestSplitMediaPath(t *testing.T) {
	id := uuid.New()

	gotID, file, ok := splitMediaPath("/media/" + id.String() + "/manifest.mpd")
	assert.True(t, ok)
	assert.Equal(t, id, gotID)
	assert.Equal(t, "manifest.mpd", file)

	for _, bad := range []string{
		"/media/" + id.String(),
		"/media/" + id.String() + "/",
		"/media/" + id.String() + "/a/b",
		"/media/" + id.String() + "/../other/x",
		"/media/not-a-uuid/manifest.mpd",
		"/media/" + id.String() + "/.hidden",
	} {
		_, _, ok := splitMediaPath(bad)
		assert.False(t, ok, bad)
	}
}

func TestContentType(t *testing.T) {
	assert.Equal(t, "application/dash+xml", ContentType("manifest.mpd"))
	assert.Equal(t, "video/iso.segment", ContentType("chunk-0-00001.m4s"))
	assert.Equal(t, "video/webm", ContentType("init-0.webm"))
	assert.Equal(t, "application/octet-stream", ContentType("weird.bin"))
}
