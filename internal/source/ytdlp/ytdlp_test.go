package ytdlp

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/anesthetised/couchcast/internal/source"
)

func TestKey(t *testing.T) {
	e := New("yt-dlp", nil, nil)
	cases := map[string]string{
		"https://www.youtube.com/watch?v=aqz-KE-bpKQ":                 "youtube:aqz-KE-bpKQ",
		"https://www.youtube.com/watch?list=PL123&v=aqz-KE-bpKQ&t=10": "youtube:aqz-KE-bpKQ",
		"https://youtu.be/aqz-KE-bpKQ?si=abc":                         "youtube:aqz-KE-bpKQ",
		"https://www.youtube.com/shorts/aqz-KE-bpKQ":                  "youtube:aqz-KE-bpKQ",
		"https://m.youtube.com/watch?v=aqz-KE-bpKQ":                   "youtube:aqz-KE-bpKQ",
		"HTTPS://Example.COM/video.mp4#t=5":                           "url:https://example.com/video.mp4",
	}
	for in, want := range cases {
		got, ok := e.Key(in)
		assert.True(t, ok, in)
		assert.Equal(t, want, got, in)
	}

	for _, bad := range []string{"not a url", "ftp://x/y", "/relative", ""} {
		_, ok := e.Key(bad)
		assert.False(t, ok, bad)
	}
}

func TestParseInfoFixture(t *testing.T) {
	data, err := os.ReadFile("testdata/youtube_bbb.json")
	require.NoError(t, err)

	p, err := ParseInfo(data)
	require.NoError(t, err)
	assert.Equal(t, "Big Buck Bunny 60fps 4K - Official Blender Foundation Short Film", p.Title)
	assert.EqualValues(t, 635_000, p.DurationMs)
	assert.NotEmpty(t, p.ThumbnailURL)

	for _, f := range p.Formats {
		assert.NotContains(t, f.ID, "-drc")
		assert.True(t, f.IsVideo() || f.IsAudio(), f.ID)
	}

	sel, err := source.SelectFormats(p, []int{1080, 720, 480, 360})
	require.NoError(t, err)
	assert.Equal(t, []string{"303", "302", "244", "243", "251"}, sel.IDs(), "VP9 per height + best opus")
	assert.Equal(t, "webm", sel.Video[0].Ext)
	assert.EqualValues(t, 168736189, sel.Video[0].Filesize)

	_, err = ParseInfo([]byte(`{"formats":[]}`))
	assert.Error(t, err)
}

func TestProgressTracker(t *testing.T) {
	var got []float64
	// 300 + 100 bytes: first file is 75% of the batch.
	tr := newProgressTracker([]int64{300, 100}, func(v float64) { got = append(got, v) })

	tr.line("noise")
	tr.line("cc-progress:150/300")
	tr.line("cc-progress:300/300")
	tr.line("cc-progress:25/100") // bytes dropped: second file started
	tr.line("cc-progress:100/100")

	assert.InDeltaSlice(t, []float64{0.375, 0.75, 0.8125, 1}, got, 1e-9)

	// Unknown sizes fall back to equal weights.
	got = nil
	tr = newProgressTracker([]int64{0, 0}, func(v float64) { got = append(got, v) })
	tr.line("cc-progress:50/100")
	tr.line("cc-progress:10/100")
	assert.InDeltaSlice(t, []float64{0.25, 0.55}, got, 1e-9)
}
