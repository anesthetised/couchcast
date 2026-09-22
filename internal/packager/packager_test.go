package packager

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestArgs(t *testing.T) {
	p := New("ffmpeg", 4)
	args := p.Args([]Input{
		{Path: "/tmp/303.webm", Width: 1920, Height: 1080},
		{Path: "/tmp/244.webm", Width: 854, Height: 480},
		{Path: "/tmp/251.webm", Audio: true},
	}, "/out")

	joined := strings.Join(args, " ")
	assert.Contains(t, joined, "-i /tmp/303.webm -i /tmp/244.webm -i /tmp/251.webm")
	assert.Contains(t, joined, "-map 0:v:0 -map 1:v:0 -map 2:a:0")
	assert.Contains(t, joined, "-c copy -f dash -seg_duration 4")
	assert.Contains(t, joined, "-adaptation_sets id=0,streams=0,1 id=1,streams=2")
	assert.Equal(t, "/out/manifest.mpd", args[len(args)-1])
	assert.NotContains(t, joined, "-c:v libx264", "never transcode")
	assert.Contains(t, joined, "-aspect:0 1920:1080 -aspect:1 1920:1080", "aspect harmonised to the largest rendition")

	noDims := p.Args([]Input{{Path: "v.mp4"}, {Path: "a.m4a", Audio: true}}, "/out")
	assert.NotContains(t, strings.Join(noDims, " "), "-aspect")
}

func TestParseProgressLine(t *testing.T) {
	ms, ok := ParseProgressLine("out_time_us=1500000")
	assert.True(t, ok)
	assert.EqualValues(t, 1500, ms)
	_, ok = ParseProgressLine("frame=12")
	assert.False(t, ok)
	_, ok = ParseProgressLine("out_time_us=N/A")
	assert.False(t, ok)
}
