package storyboard

import (
	"context"
	"image/jpeg"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInterval(t *testing.T) {
	assert.Equal(t, 5*time.Second, Interval(3*time.Minute), "short videos are clamped up")
	assert.Equal(t, 24*time.Second, Interval(2*time.Hour))
	assert.Equal(t, time.Minute, Interval(10*time.Hour), "long ones are clamped down")
}

// TestFramesAndTile runs the real ffmpeg on a generated clip; it skips
// where ffmpeg is not installed.
func TestFramesAndTile(t *testing.T) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg not installed")
	}
	dir := t.TempDir()
	clip := filepath.Join(dir, "clip.webm")
	gen := exec.Command(ffmpeg, "-y", "-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", "testsrc=duration=32:size=426x240:rate=10",
		"-c:v", "libvpx-vp9", "-g", "20", "-b:v", "200k", "-deadline", "realtime", clip) //nolint:gosec // test fixture
	out, err := gen.CombinedOutput()
	require.NoError(t, err, string(out))

	frames, err := Frames(context.Background(), ffmpeg, clip, 5*time.Second, filepath.Join(dir, "frames"))
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(frames), 6)
	require.LessOrEqual(t, len(frames), 8)

	outDir := filepath.Join(dir, "out")
	require.NoError(t, os.MkdirAll(outDir, 0o750))
	sb, err := Tile(frames, 5*time.Second, outDir)
	require.NoError(t, err)
	assert.Equal(t, int64(5000), sb.IntervalMs)
	assert.Equal(t, FrameWidth, sb.Width)
	assert.Equal(t, 90, sb.Height, "426×240 scaled to 160 wide, even height")
	assert.Equal(t, len(frames), sb.Count)
	assert.Equal(t, 1, sb.Sheets)

	f, err := os.Open(filepath.Join(outDir, SheetName(0)))
	require.NoError(t, err)
	defer func() { _ = f.Close() }()
	cfg, err := jpeg.DecodeConfig(f)
	require.NoError(t, err)
	assert.Equal(t, sb.Width*sb.Cols, cfg.Width)
	assert.Equal(t, sb.Height, cfg.Height, "a single row when fewer than ten frames")
}

func TestTileManySheets(t *testing.T) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg not installed")
	}
	dir := t.TempDir()
	// 230 tiny frames straight from lavfi, no clip needed.
	gen := exec.Command(ffmpeg, "-y", "-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", "testsrc=duration=23:size=160x90:rate=10",
		filepath.Join(dir, "f-%05d.jpg")) //nolint:gosec // test fixture
	out, err := gen.CombinedOutput()
	require.NoError(t, err, string(out))
	frames, err := filepath.Glob(filepath.Join(dir, "f-*.jpg"))
	require.NoError(t, err)
	require.Len(t, frames, 230)

	sb, err := Tile(frames, 5*time.Second, dir)
	require.NoError(t, err)
	assert.Equal(t, 3, sb.Sheets)
	f, err := os.Open(filepath.Join(dir, SheetName(2)))
	require.NoError(t, err)
	defer func() { _ = f.Close() }()
	cfg, err := jpeg.DecodeConfig(f)
	require.NoError(t, err)
	assert.Equal(t, 3*90, cfg.Height, "the last sheet holds 30 frames: three rows")
}
