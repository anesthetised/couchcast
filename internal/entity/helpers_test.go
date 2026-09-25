package entity

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestMediaInProgress(t *testing.T) {
	for status, want := range map[MediaStatus]bool{
		MediaQueued: true, MediaProbing: true, MediaDownloading: true, MediaPackaging: true, MediaUploading: true,
		MediaReady: false, MediaFailed: false,
	} {
		assert.Equal(t, want, (&Media{Status: status}).InProgress(), status)
	}
}

func TestRoomMuteActive(t *testing.T) {
	now := time.Now()
	assert.True(t, (&RoomMute{Until: now.Add(time.Minute)}).Active(now))
	assert.False(t, (&RoomMute{Until: now}).Active(now))
	var none *RoomMute
	assert.False(t, none.Active(now))
}
