// Package packager turns downloaded elementary streams into a DASH
// presentation with ffmpeg. Streams are copied, never transcoded: the
// source already provides one file per quality.
package packager

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// ManifestName is the MPD file name inside the output directory.
const ManifestName = "manifest.mpd"

// Input is one elementary stream to include. Width and Height are used to
// harmonise display aspect ratios across video renditions; leave them 0
// when unknown.
type Input struct {
	Path   string
	Audio  bool
	Width  int
	Height int
}

// Packager wraps the ffmpeg binary.
type Packager struct {
	FFmpeg         string
	SegmentSeconds int
}

// New creates a packager.
func New(ffmpeg string, segmentSeconds int) *Packager {
	if segmentSeconds <= 0 {
		segmentSeconds = 4
	}
	return &Packager{FFmpeg: ffmpeg, SegmentSeconds: segmentSeconds}
}

// Args builds the ffmpeg argument list. Video inputs become one adaptation
// set with one representation each (ids 0..n-1 in input order); the audio
// input becomes a second adaptation set. Segment type follows the codecs:
// WebM for VP9/Opus, fMP4 for H.264/AAC.
func (p *Packager) Args(inputs []Input, outDir string) []string {
	args := []string{"-y", "-hide_banner", "-loglevel", "error", "-nostdin"}

	for _, in := range inputs {
		args = append(args, "-i", in.Path)
	}

	var videoStreams, audioStreams []string
	for i, in := range inputs {
		if in.Audio {
			args = append(args, "-map", strconv.Itoa(i)+":a:0")
			audioStreams = append(audioStreams, strconv.Itoa(len(videoStreams)+len(audioStreams)))
		} else {
			args = append(args, "-map", strconv.Itoa(i)+":v:0")
			videoStreams = append(videoStreams, strconv.Itoa(len(videoStreams)+len(audioStreams)))
		}
	}

	// The DASH muxer refuses representations with different display aspect
	// ratios in one adaptation set, and sources round small renditions
	// (854x480 is not exactly 16:9). Declare the largest rendition's ratio
	// on every video stream; this only rewrites container metadata.
	if refW, refH, ok := referenceAspect(inputs); ok {
		aspect := strconv.Itoa(refW) + ":" + strconv.Itoa(refH)
		for _, idx := range videoStreams {
			args = append(args, "-aspect:"+idx, aspect)
		}
	}

	sets := "id=0,streams=" + strings.Join(videoStreams, ",")
	if len(audioStreams) > 0 {
		sets += " id=1,streams=" + strings.Join(audioStreams, ",")
	}

	args = append(args,
		"-c", "copy",
		"-f", "dash",
		"-seg_duration", strconv.Itoa(p.SegmentSeconds),
		"-use_timeline", "1",
		"-use_template", "1",
		"-dash_segment_type", "auto",
		"-adaptation_sets", sets,
		"-init_seg_name", "init-$RepresentationID$.$ext$",
		"-media_seg_name", "chunk-$RepresentationID$-$Number%05d$.$ext$",
		filepath.Join(outDir, ManifestName),
	)

	return args
}

// Run executes ffmpeg and returns a readable error with ffmpeg's last
// stderr lines on failure.
func (p *Packager) Run(ctx context.Context, inputs []Input, outDir string) error {
	cmd := exec.CommandContext(ctx, p.FFmpeg, p.Args(inputs, outDir)...) //nolint:gosec // binary from config, args built here
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("ffmpeg: %w: %s", err, tail(stderr.String(), 3))
	}
	return nil
}

// referenceAspect returns the dimensions of the largest video input.
func referenceAspect(inputs []Input) (w, h int, ok bool) {
	for _, in := range inputs {
		if !in.Audio && in.Width > 0 && in.Height > 0 && in.Height > h {
			w, h, ok = in.Width, in.Height, true
		}
	}
	return w, h, ok
}

func tail(s string, n int) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, " | ")
}
