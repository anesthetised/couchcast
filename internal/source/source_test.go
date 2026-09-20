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
	assert.Equal(t, []string{"248", "247b", "244"}, ids, "VP9 preferred, best bitrate per height, 144p is no stand-in for 360")
	assert.Equal(t, "251", sel.Audio.ID, "best opus")
	assert.Equal(t, []string{"248", "247b", "244", "251"}, sel.IDs())

	// Without VP9 fall back to H.264 and AAC.
	h264 := &Probe{Formats: []Format{probe.Formats[1], probe.Formats[2], probe.Formats[8]}}
	sel, err = SelectFormats(h264, []int{1080, 720})
	require.NoError(t, err)
	assert.Equal(t, "137", sel.Video[0].ID)
	assert.Equal(t, "140", sel.Audio.ID)

	// Off-ladder heights map to the nearest rung, each height used once.
	odd := &Probe{Formats: []Format{
		{ID: "a", VCodec: "vp09", Height: 872, Bitrate: 3000},
		{ID: "b", VCodec: "vp09", Height: 818, Bitrate: 2500},
		{ID: "c", VCodec: "vp09", Height: 534, Bitrate: 1000},
		{ID: "d", VCodec: "vp09", Height: 356, Bitrate: 600},
		{ID: "e", VCodec: "vp09", Height: 178, Bitrate: 200},
		probe.Formats[9],
	}}
	sel, err = SelectFormats(odd, []int{1080, 720, 480, 360})
	require.NoError(t, err)
	assert.Equal(t, []string{"a", "b", "c", "d", "251"}, sel.IDs(), "872→1080, 818→720, 534→480, 356→360")
	sel, err = SelectFormats(probe, []int{2160})
	require.NoError(t, err)
	assert.Equal(t, []string{"248"}, []string{sel.Video[0].ID}, "a single rung takes the closest height")
	assert.Len(t, sel.Video, 1)

	// A tiny video still plays through the lowest rung.
	tiny := &Probe{Formats: []Format{probe.Formats[7], probe.Formats[9]}}
	sel, err = SelectFormats(tiny, []int{1080, 720, 480, 360})
	require.NoError(t, err)
	assert.Equal(t, []string{"278", "251"}, sel.IDs())

	// No video formats at all.
	_, err = SelectFormats(&Probe{Formats: []Format{probe.Formats[9]}}, []int{1080})
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
