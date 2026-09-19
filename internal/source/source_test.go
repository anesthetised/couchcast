package source

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSelectFormats(t *testing.T) {
	probe := &Probe{Formats: []Format{
		{ID: "18", Ext: "mp4", VCodec: "avc1.42001E", ACodec: "mp4a.40.2", Height: 360, Bitrate: 500}, // muxed: ignored
		{ID: "137", Ext: "mp4", VCodec: "avc1.640028", Height: 1080, Bitrate: 4000},
		{ID: "136", Ext: "mp4", VCodec: "avc1.4d401f", Height: 720, Bitrate: 2000},
		{ID: "248", Ext: "webm", VCodec: "vp09.00.40.08", Height: 1080, Bitrate: 3000},
		{ID: "247", Ext: "webm", VCodec: "vp09.00.31.08", Height: 720, Bitrate: 1500},
		{ID: "247b", Ext: "webm", VCodec: "vp09.00.31.08", Height: 720, Bitrate: 1800},
		{ID: "244", Ext: "webm", VCodec: "vp09.00.21.08", Height: 480, Bitrate: 800},
		{ID: "278", Ext: "webm", VCodec: "vp09.00.10.08", Height: 144, Bitrate: 100},
		{ID: "140", Ext: "m4a", ACodec: "mp4a.40.2", Bitrate: 128},
		{ID: "251", Ext: "webm", ACodec: "opus", Bitrate: 160},
		{ID: "250", Ext: "webm", ACodec: "opus", Bitrate: 70},
	}}

	sel, err := SelectFormats(probe, []int{1080, 720, 480, 360})
	require.NoError(t, err)

	ids := make([]string, 0, len(sel.Video))
	for _, v := range sel.Video {
		ids = append(ids, v.ID)
	}
	assert.Equal(t, []string{"248", "247b", "244"}, ids, "VP9 preferred, best bitrate per height, 360 skipped")
	assert.Equal(t, "251", sel.Audio.ID, "best opus")
	assert.Equal(t, []string{"248", "247b", "244", "251"}, sel.IDs())

	// Without VP9 fall back to H.264 and AAC.
	h264 := &Probe{Formats: []Format{probe.Formats[1], probe.Formats[2], probe.Formats[8]}}
	sel, err = SelectFormats(h264, []int{1080, 720})
	require.NoError(t, err)
	assert.Equal(t, "137", sel.Video[0].ID)
	assert.Equal(t, "140", sel.Audio.ID)

	// Ladder with no matching heights.
	_, err = SelectFormats(probe, []int{2160})
	assert.Error(t, err)

	// No audio.
	_, err = SelectFormats(&Probe{Formats: probe.Formats[:8]}, []int{1080})
	assert.Error(t, err)
}

func TestCodecFamily(t *testing.T) {
	assert.Equal(t, "vp09", Format{VCodec: "vp09.00.40.08"}.CodecFamily())
	assert.Equal(t, "opus", Format{ACodec: "opus"}.CodecFamily())
	assert.Equal(t, "mp4a", Format{ACodec: "mp4a.40.2"}.CodecFamily())
}
