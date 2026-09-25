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
		"https://WWW.YouTube.com./watch?v=aqz-KE-bpKQ":                "youtube:aqz-KE-bpKQ",
		"HTTPS://Example.COM/video.mp4#t=5":                           "url:https://example.com/video.mp4",
		// The id is only trusted on YouTube's own hosts: these are fetched
		// from elsewhere and must go through the address check.
		"http://169.254.169.254/youtube.com/watch?v=aqz-KE-bpKQ": "url:http://169.254.169.254/youtube.com/watch?v=aqz-KE-bpKQ",
		"http://10.0.0.1/?u=https://youtu.be/aqz-KE-bpKQ":        "url:http://10.0.0.1/?u=https://youtu.be/aqz-KE-bpKQ",
		"https://10.0.0.1#www.youtube.com/watch?v=aqz-KE-bpKQ":   "url:https://10.0.0.1",
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

func TestPickSubtitles(t *testing.T) {
	// Uploaded tracks win, sorted and capped; live chat is skipped.
	info := infoJSON{
		Language:          "en",
		Subtitles:         map[string][]subtitleJSON{"ru": {{Ext: "vtt", Name: "Russian"}}, "en": {{Ext: "vtt", Name: "English"}}, "en-live_chat": {{Ext: "json"}}},
		AutomaticCaptions: map[string][]subtitleJSON{"en": {{Ext: "vtt", Name: "English"}}, "de": {{Ext: "vtt"}}},
	}
	subs := pickSubtitles(info)
	require.Len(t, subs, 2)
	assert.Equal(t, "en", subs[0].Lang)
	assert.Equal(t, "ru", subs[1].Lang)
	assert.False(t, subs[0].Auto)

	// Without uploaded tracks: the automatic ones in the original language only.
	info.Subtitles = nil
	subs = pickSubtitles(info)
	require.Len(t, subs, 1)
	assert.Equal(t, source.Subtitle{Lang: "en", Name: "English", Auto: true}, subs[0])

	// No original language known: nothing.
	info.Language = ""
	assert.Empty(t, pickSubtitles(info))

	// The cap.
	info.Subtitles = map[string][]subtitleJSON{}
	for i := range 20 {
		info.Subtitles[string(rune('a'+i))+"x"] = []subtitleJSON{{Ext: "vtt"}}
	}
	assert.Len(t, pickSubtitles(info), maxSubtitleTracks)
}

func TestChapters(t *testing.T) {
	info := infoJSON{Chapters: []chapterJSON{
		{StartTime: 0, EndTime: 12.5, Title: "Intro"},
		{StartTime: 12.5, EndTime: 12.5, Title: "empty"}, // zero length
		{StartTime: 12.5, EndTime: 60, Title: "  "},      // no title
		{StartTime: 60, EndTime: 635, Title: "Main"},
		{StartTime: 30, EndTime: 40, Title: "out of order"},
	}}
	got := chapters(info)
	require.Len(t, got, 2)
	assert.Equal(t, source.Chapter{StartMs: 0, EndMs: 12500, Title: "Intro"}, got[0])
	assert.Equal(t, source.Chapter{StartMs: 60000, EndMs: 635000, Title: "Main"}, got[1])

	// A single chapter is not a table of contents.
	assert.Nil(t, chapters(infoJSON{Chapters: []chapterJSON{{EndTime: 10, Title: "All"}}}))
	assert.Nil(t, chapters(infoJSON{}))
}

func TestPlaylistURL(t *testing.T) {
	e := New("yt-dlp", nil, nil)
	pl, video, ok := e.PlaylistURL("https://www.youtube.com/playlist?list=PLabcdefghij123")
	assert.True(t, ok)
	assert.False(t, video)
	assert.Equal(t, "https://www.youtube.com/playlist?list=PLabcdefghij123", pl)

	pl, video, ok = e.PlaylistURL("https://youtube.com/watch?v=aqz-KE-bpKQ&list=PLabcdefghij123&index=2")
	assert.True(t, ok)
	assert.True(t, video, "a watch link inside a playlist")
	assert.Equal(t, "https://www.youtube.com/playlist?list=PLabcdefghij123", pl)

	for _, raw := range []string{
		"https://www.youtube.com/watch?v=aqz-KE-bpKQ",
		"https://www.youtube.com/watch?v=aqz-KE-bpKQ&list=RDaqz-KE-bpKQ", // a mix
		"https://vimeo.com/showcase/123?list=PLabcdefghij123",
	} {
		_, _, ok := e.PlaylistURL(raw)
		assert.False(t, ok, raw)
	}
}

func TestParsePlaylist(t *testing.T) {
	data := []byte(`{"_type": "playlist", "title": "Cooking basics", "playlist_count": 12, "entries": [
		{"_type": "url", "id": "a1", "url": "https://www.youtube.com/watch?v=a1", "title": "Knife skills", "duration": 312,
		 "thumbnails": [{"url": "https://i.ytimg.com/vi/a1/small.jpg"}, {"url": "https://i.ytimg.com/vi/a1/big.jpg"}]},
		{"_type": "url", "id": "a2", "url": "https://www.youtube.com/watch?v=a2", "title": "[Private video]", "duration": null},
		{"_type": "url", "id": "a3", "url": "https://www.youtube.com/watch?v=a3", "title": "Stocks and sauces", "duration": 605.5},
		{"_type": "url", "id": "a4", "url": "https://www.youtube.com/watch?v=a4", "title": "Bread", "duration": 100}
	]}`)
	pl, err := ParsePlaylist(data, 2)
	require.NoError(t, err)
	assert.Equal(t, "Cooking basics", pl.Title)
	assert.Equal(t, 12, pl.Total)
	require.Len(t, pl.Entries, 2, "private entries are skipped, the limit applies to what is left")
	assert.Equal(t, source.PlaylistEntry{URL: "https://www.youtube.com/watch?v=a1", Title: "Knife skills", DurationMs: 312_000, ThumbnailURL: "https://i.ytimg.com/vi/a1/big.jpg"}, pl.Entries[0])
	assert.EqualValues(t, 605_500, pl.Entries[1].DurationMs)

	_, err = ParsePlaylist([]byte(`{"_type": "video", "title": "x"}`), 10)
	assert.Error(t, err)
}
