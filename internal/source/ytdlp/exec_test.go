package ytdlp

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/anesthetised/couchcast/internal/source"
)

// stub writes a fake yt-dlp: it records its arguments, prints recorded
// JSON for -J, writes the files a real run would, and fails for URLs
// containing "gone" the way yt-dlp does.
const stub = `#!/bin/sh
printf '%s\n' "$@" > "$STUB_ARGS"
for a in "$@"; do last="$a"; done
case "$last" in *gone*) echo "ERROR: [youtube] gone: Video unavailable" >&2; exit 1;; esac
out=""; formats=""; flat=""; json=""; skip=""
while [ $# -gt 0 ]; do
  case "$1" in
    -o) out="$2"; shift;;
    -f) formats="$2"; shift;;
    -J) json=1;;
    --flat-playlist) flat=1;;
    --skip-download) skip=1;;
  esac
  shift
done
if [ -n "$json" ] && [ -n "$flat" ]; then
  echo '{"_type":"playlist","title":"Mix","playlist_count":3,"entries":[{"url":"https://www.youtube.com/watch?v=a1","title":"One","duration":60}]}'
  exit 0
fi
if [ -n "$json" ]; then cat "$STUB_INFO"; exit 0; fi
dir=$(dirname "$out")
if [ -n "$skip" ]; then
  printf 'WEBVTT\n' > "$dir/subs.en.vtt"
  printf 'WEBVTT\n' > "$dir/subs.de-orig.vtt"
  exit 0
fi
for id in $(echo "$formats" | tr ',' ' '); do
  echo "cc-progress:0/100"
  echo "cc-progress:50/100"
  echo "some other line"
  echo "cc-progress:100/100"
  printf 'data' > "$dir/$id.webm"
done
`

func newStub(t *testing.T, extra ...string) (*Extractor, func() []string) {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "yt-dlp")
	require.NoError(t, os.WriteFile(bin, []byte(stub), 0o700)) //nolint:gosec // executable test stub
	argsFile := filepath.Join(dir, "args")
	info, err := filepath.Abs("testdata/youtube_bbb.json")
	require.NoError(t, err)
	t.Setenv("STUB_ARGS", argsFile)
	t.Setenv("STUB_INFO", info)
	args := func() []string {
		b, err := os.ReadFile(argsFile) //nolint:gosec // test temp file
		require.NoError(t, err)
		return strings.Split(strings.TrimSpace(string(b)), "\n")
	}
	return New(bin, extra, slog.New(slog.DiscardHandler)), args
}

func TestProbeRunsYtDlp(t *testing.T) {
	e, args := newStub(t, "--cookies", "/run/secrets/c.txt")
	p, err := e.Probe(context.Background(), "https://www.youtube.com/watch?v=aqz-KE-bpKQ")
	require.NoError(t, err)
	assert.NotEmpty(t, p.Title)
	assert.NotEmpty(t, p.Formats)
	// Extra flags go before the URL, which always follows "--".
	assert.Equal(t, []string{"-J", "--no-playlist", "--no-warnings", "--cookies", "/run/secrets/c.txt", "--", "https://www.youtube.com/watch?v=aqz-KE-bpKQ"}, args())

	_, err = e.Probe(context.Background(), "https://www.youtube.com/watch?v=gone")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Video unavailable", "the last stderr line explains the failure")
}

func TestPlaylistRunsYtDlp(t *testing.T) {
	e, args := newStub(t)
	pl, err := e.Playlist(context.Background(), "https://www.youtube.com/playlist?list=PL1", 25)
	require.NoError(t, err)
	assert.Equal(t, "Mix", pl.Title)
	require.Len(t, pl.Entries, 1)
	assert.Contains(t, args(), "25", "the limit is passed as --playlist-end")
	_, err = e.Playlist(context.Background(), "https://www.youtube.com/playlist?list=gone", 25)
	assert.Error(t, err)
}

func TestDownloadRunsYtDlp(t *testing.T) {
	e, args := newStub(t)
	dir := t.TempDir()
	var reports []float64
	files, err := e.Download(context.Background(), "https://youtu.be/x", []source.Format{
		{ID: "248", Filesize: 300},
		{ID: "251", Filesize: 100},
	}, dir, func(v float64) { reports = append(reports, v) })
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"248": filepath.Join(dir, "248.webm"), "251": filepath.Join(dir, "251.webm")}, files)
	assert.Contains(t, args(), "248,251")
	require.NotEmpty(t, reports)
	assert.InDelta(t, 1, reports[len(reports)-1], 0.001)
	for i := 1; i < len(reports); i++ {
		assert.GreaterOrEqual(t, reports[i], reports[i-1], "progress never goes back")
	}

	_, err = e.Download(context.Background(), "https://youtu.be/gone", []source.Format{{ID: "248"}}, t.TempDir(), func(float64) {})
	assert.Error(t, err)
}

func TestDownloadSubtitlesRunsYtDlp(t *testing.T) {
	e, args := newStub(t)
	dir := t.TempDir()
	files, err := e.DownloadSubtitles(context.Background(), "https://youtu.be/x", []source.Subtitle{
		{Lang: "en"}, {Lang: "de", Auto: true}, {Lang: "fr"},
	}, dir)
	require.NoError(t, err)
	assert.Equal(t, map[string]string{
		"en": filepath.Join(dir, "subs.en.vtt"),
		"de": filepath.Join(dir, "subs.de-orig.vtt"),
	}, files, "an auto track may arrive as <lang>-orig; missing ones are absent")
	a := args()
	assert.Contains(t, a, "en,de,fr")
	assert.Contains(t, a, "--write-auto-subs")

	none, err := e.DownloadSubtitles(context.Background(), "https://youtu.be/x", nil, dir)
	require.NoError(t, err)
	assert.Empty(t, none)
	_, err = e.DownloadSubtitles(context.Background(), "https://youtu.be/gone", []source.Subtitle{{Lang: "en"}}, dir)
	assert.Error(t, err)
}
