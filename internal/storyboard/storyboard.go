// Package storyboard builds the timeline preview sheets: frames taken at
// a fixed interval, tiled into JPEG grids the player crops from.
package storyboard

import (
	"context"
	"fmt"
	"image"
	"image/draw"
	"image/jpeg"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"time"

	"github.com/anesthetised/couchcast/internal/entity"
)

const (
	// FrameWidth is the preview width in CSS pixels.
	FrameWidth = 160
	cols       = 10
	rows       = 10
	// targetFrames keeps long videos to a few sheets; the interval is
	// clamped so short ones do not repeat the same keyframe and long ones
	// still show something every minute.
	targetFrames = 300
	minInterval  = 5 * time.Second
	maxInterval  = time.Minute
)

// Interval picks the spacing between frames for a video length.
func Interval(duration time.Duration) time.Duration {
	iv := (duration / targetFrames).Round(time.Second)
	return min(max(iv, minInterval), maxInterval)
}

// SheetName is the object name of sheet n inside the media prefix.
func SheetName(n int) string { return "sb-" + strconv.Itoa(n) + ".jpg" }

// Frames runs ffmpeg over the smallest rendition, decoding keyframes only
// (fast even for a feature film), and writes one JPEG per interval into
// dir. It returns the frame files in order.
func Frames(ctx context.Context, ffmpeg, input string, interval time.Duration, dir string) ([]string, error) {
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, err
	}
	vf := fmt.Sprintf("fps=1/%d,scale=%d:-2", int(interval.Seconds()), FrameWidth)
	args := []string{"-y", "-hide_banner", "-loglevel", "error", "-nostdin",
		"-skip_frame", "nokey", "-i", input, "-an", "-sn", "-vf", vf, "-q:v", "6",
		filepath.Join(dir, "f-%05d.jpg")}
	cmd := exec.CommandContext(ctx, ffmpeg, args...) //nolint:gosec // binary from config, args built here
	if out, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("storyboard frames: %w: %s", err, out)
	}
	frames, err := filepath.Glob(filepath.Join(dir, "f-*.jpg"))
	if err != nil {
		return nil, err
	}
	sort.Strings(frames)
	return frames, nil
}

// Tile packs frames into sheets of cols×rows in outDir (sb-0.jpg, …) and
// returns the description the player needs. All frames share the size of
// the first one.
func Tile(frames []string, interval time.Duration, outDir string) (*entity.Storyboard, error) {
	if len(frames) == 0 {
		return nil, fmt.Errorf("storyboard: no frames")
	}
	first, err := load(frames[0])
	if err != nil {
		return nil, err
	}
	w, h := first.Bounds().Dx(), first.Bounds().Dy()
	perSheet := cols * rows
	sheets := (len(frames) + perSheet - 1) / perSheet
	for s := range sheets {
		n := min(perSheet, len(frames)-s*perSheet)
		usedRows := (n + cols - 1) / cols
		sheet := image.NewRGBA(image.Rect(0, 0, w*cols, h*usedRows))
		for i := range n {
			img := first
			if s > 0 || i > 0 {
				if img, err = load(frames[s*perSheet+i]); err != nil {
					return nil, err
				}
			}
			at := image.Pt((i%cols)*w, (i/cols)*h)
			draw.Draw(sheet, image.Rectangle{Min: at, Max: at.Add(image.Pt(w, h))}, img, img.Bounds().Min, draw.Src)
		}
		if err := save(filepath.Join(outDir, SheetName(s)), sheet); err != nil {
			return nil, err
		}
	}
	return &entity.Storyboard{
		IntervalMs: interval.Milliseconds(), Width: w, Height: h,
		Cols: cols, Rows: rows, Count: len(frames), Sheets: sheets,
	}, nil
}

func load(path string) (image.Image, error) {
	f, err := os.Open(path) //nolint:gosec // our own frame files
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	img, _, err := image.Decode(f)
	return img, err
}

func save(path string, img image.Image) error {
	f, err := os.Create(path) //nolint:gosec // our own output directory
	if err != nil {
		return err
	}
	if err := jpeg.Encode(f, img, &jpeg.Options{Quality: 70}); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}
