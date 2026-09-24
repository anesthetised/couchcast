// Package webvtt tidies subtitle files before they are stored.
//
// YouTube's automatic captions come in a "roll-up" layout meant for its
// own player: every cue repeats the previous line above the new one,
// words carry karaoke timestamps (<00:00:01.599><c> word</c>), 10 ms
// cues bridge the lines, a blank line pads the top and every cue is
// pinned to the left edge (align:start position:0%). Rendered as plain
// WebVTT that jumps, flickers and hugs the left border. Normalize turns
// it into ordinary pop-on subtitles: up to two lines per cue, each line
// shown once, centred. Other files only lose YouTube's left-edge pin and
// padding lines; their timing and text stay as they are.
package webvtt

import (
	"bytes"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type cue struct {
	id         string
	start, end time.Duration
	settings   string
	lines      []string
}

var (
	inlineTimestamp = regexp.MustCompile(`<\d{1,2}:\d{2}(?::\d{2})?\.\d{3}>`)
	tag             = regexp.MustCompile(`<[^>]*>`)
	spaces          = regexp.MustCompile(`\s+`)
)

const (
	// pairGap is the longest pause between two caption lines that still
	// lets them share one cue; after a longer pause the next line starts
	// a new cue so it does not show up before it is spoken.
	pairGap = time.Second
	// maxLines per cue in the roll-up conversion.
	maxLines = 2
)

// Normalize rewrites a WebVTT file as described in the package comment.
// Input it cannot parse comes back unchanged.
func Normalize(data []byte) []byte {
	text := strings.ReplaceAll(string(data), "\r\n", "\n")
	blocks := strings.Split(strings.TrimSpace(text), "\n\n")
	if len(blocks) == 0 || !strings.HasPrefix(blocks[0], "WEBVTT") {
		return data
	}
	header := blocks[0]

	var (
		cues   []cue
		extras []string // NOTE / STYLE / REGION blocks, kept for plain files
		rollUp bool
	)
	for _, b := range blocks[1:] {
		b = strings.Trim(b, "\n")
		if b == "" {
			continue
		}
		c, ok := parseCue(b)
		if !ok {
			extras = append(extras, b)
			continue
		}
		for _, l := range c.lines {
			if inlineTimestamp.MatchString(l) {
				rollUp = true
			}
		}
		cues = append(cues, c)
	}

	if rollUp {
		cues = fromRollUp(cues)
		extras = nil
	} else {
		cues = tidy(cues)
	}

	var out bytes.Buffer
	out.WriteString(header)
	out.WriteString("\n\n")
	for _, e := range extras {
		out.WriteString(e)
		out.WriteString("\n\n")
	}
	for _, c := range cues {
		if c.id != "" {
			out.WriteString(c.id + "\n")
		}
		fmt.Fprintf(&out, "%s --> %s", stamp(c.start), stamp(c.end))
		if c.settings != "" {
			out.WriteString(" " + c.settings)
		}
		out.WriteString("\n")
		out.WriteString(strings.Join(c.lines, "\n"))
		out.WriteString("\n\n")
	}
	return out.Bytes()
}

// fromRollUp keeps the line each roll-up cue introduces (the one with
// word timestamps), drops the repeats and bridges, and pairs consecutive
// lines into two-line cues.
func fromRollUp(in []cue) []cue {
	type piece struct {
		start, end time.Duration
		text       string
	}
	var pieces []piece
	for _, c := range in {
		for i := len(c.lines) - 1; i >= 0; i-- {
			if !inlineTimestamp.MatchString(c.lines[i]) {
				continue
			}
			if t := clean(c.lines[i]); t != "" {
				pieces = append(pieces, piece{start: c.start, end: c.end, text: t})
			}
			break
		}
	}

	out := make([]cue, 0, len(pieces)/maxLines+1)
	for i := 0; i < len(pieces); {
		group := []piece{pieces[i]}
		i++
		for len(group) < maxLines && i < len(pieces) && pieces[i].start-group[len(group)-1].end <= pairGap {
			group = append(group, pieces[i])
			i++
		}
		c := cue{start: group[0].start, end: group[len(group)-1].end}
		for _, p := range group {
			c.lines = append(c.lines, p.text)
		}
		out = append(out, c)
	}
	return out
}

// tidy drops YouTube's left-edge pin and blank padding lines from an
// ordinary file.
func tidy(in []cue) []cue {
	out := in[:0]
	for _, c := range in {
		if youTubePin(c.settings) {
			c.settings = ""
		}
		lines := c.lines[:0]
		for _, l := range c.lines {
			if strings.TrimSpace(l) != "" {
				lines = append(lines, strings.TrimRight(l, " \t"))
			}
		}
		if len(lines) == 0 {
			continue
		}
		c.lines = lines
		out = append(out, c)
	}
	return out
}

func youTubePin(settings string) bool {
	f := strings.Fields(settings)
	if len(f) != 2 {
		return false
	}
	has := map[string]bool{f[0]: true, f[1]: true}
	return has["align:start"] && has["position:0%"]
}

// clean strips tags and squeezes whitespace.
func clean(s string) string {
	return strings.TrimSpace(spaces.ReplaceAllString(tag.ReplaceAllString(s, ""), " "))
}

func parseCue(block string) (cue, bool) {
	lines := strings.Split(block, "\n")
	var c cue
	i := 0
	if !strings.Contains(lines[0], "-->") {
		if len(lines) < 2 || !strings.Contains(lines[1], "-->") {
			return cue{}, false
		}
		c.id = lines[0]
		i = 1
	}
	left, right, _ := strings.Cut(lines[i], "-->")
	start, ok1 := parseStamp(strings.TrimSpace(left))
	fields := strings.Fields(right)
	if !ok1 || len(fields) == 0 {
		return cue{}, false
	}
	end, ok2 := parseStamp(fields[0])
	if !ok2 {
		return cue{}, false
	}
	c.start, c.end = start, end
	c.settings = strings.Join(fields[1:], " ")
	c.lines = lines[i+1:]
	return c, true
}

// parseStamp reads hh:mm:ss.ttt or mm:ss.ttt.
func parseStamp(s string) (time.Duration, bool) {
	main, frac, ok := strings.Cut(s, ".")
	if !ok || len(frac) != 3 {
		return 0, false
	}
	parts := strings.Split(main, ":")
	if len(parts) < 2 || len(parts) > 3 {
		return 0, false
	}
	var total int64
	for _, p := range parts {
		n, err := strconv.ParseInt(p, 10, 64)
		if err != nil || n < 0 {
			return 0, false
		}
		total = total*60 + n
	}
	ms, err := strconv.ParseInt(frac, 10, 64)
	if err != nil {
		return 0, false
	}
	return time.Duration(total)*time.Second + time.Duration(ms)*time.Millisecond, true
}

func stamp(d time.Duration) string {
	ms := d.Milliseconds()
	return fmt.Sprintf("%02d:%02d:%02d.%03d", ms/3_600_000, ms/60_000%60, ms/1000%60, ms%1000)
}
