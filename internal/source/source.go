// Package source abstracts where videos come from. An Extractor turns a URL
// into metadata and downloadable formats; the ingest worker is agnostic to
// the site behind it.
package source

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
)

// ErrUnsupported is returned when no extractor handles a URL.
var ErrUnsupported = errors.New("source: unsupported url")

// Format is one downloadable stream (video-only or audio-only).
type Format struct {
	ID       string // extractor-specific id, passed back to Download
	Ext      string // container extension: webm, mp4, m4a
	VCodec   string // e.g. vp09.00.40.08, avc1.640028, av01...; "" for audio
	ACodec   string // e.g. opus, mp4a.40.2; "" for video
	Height   int
	Width    int
	Bitrate  int   // kbit/s (total for the stream)
	Filesize int64 // bytes, may be an estimate or 0
}

// IsVideo reports whether the format carries a video track.
func (f Format) IsVideo() bool { return f.VCodec != "" }

// IsAudio reports whether the format carries an audio track.
func (f Format) IsAudio() bool { return f.ACodec != "" }

// CodecFamily returns the leading codec tag: vp09, avc1, av01, opus, mp4a.
// Extractors report VP9 both as "vp9" and "vp09.xx"; both map to vp09.
func (f Format) CodecFamily() string {
	c := f.VCodec
	if c == "" {
		c = f.ACodec
	}
	if i := strings.IndexByte(c, '.'); i >= 0 {
		c = c[:i]
	}
	switch c {
	case "vp9":
		return "vp09"
	case "vp8":
		return "vp08"
	}
	return c
}

// Subtitle is a text track the extractor can fetch.
type Subtitle struct {
	Lang string // BCP 47-ish tag as the site reports it: en, ru, pt-BR
	Name string // human name, may be empty
	Auto bool   // machine-generated captions
}

// Probe is what an extractor learns about a URL without downloading it.
type Probe struct {
	Title        string
	DurationMs   int64
	ThumbnailURL string
	Formats      []Format
	Subtitles    []Subtitle
}

// Selection is the set of formats chosen for packaging.
type Selection struct {
	Video []Format // best per ladder height, descending height
	Audio Format
}

// Formats returns the formats to download, video first.
func (s Selection) Formats() []Format {
	out := make([]Format, 0, len(s.Video)+1)
	out = append(out, s.Video...)
	return append(out, s.Audio)
}

// IDs returns the format ids to download, video first.
func (s Selection) IDs() []string {
	ids := make([]string, 0, len(s.Video)+1)
	for _, v := range s.Video {
		ids = append(ids, v.ID)
	}
	return append(ids, s.Audio.ID)
}

// Extractor resolves and downloads media from a site.
type Extractor interface {
	// Key returns a stable dedupe key for the URL without touching the
	// network ("youtube:<id>", "url:<normalized>"). ok is false when the
	// extractor does not handle the URL at all.
	Key(url string) (key string, ok bool)
	// Probe fetches metadata and the available formats.
	Probe(ctx context.Context, url string) (*Probe, error)
	// Download fetches the given formats into dir and returns a map from
	// format id to file path. progress receives 0..1 for the whole batch,
	// weighted by the formats' expected sizes when known.
	Download(ctx context.Context, url string, formats []Format, dir string, progress func(float64)) (map[string]string, error)
	// DownloadSubtitles fetches the given tracks as WebVTT into dir and
	// returns a map from language to file path. Missing tracks are simply
	// absent from the map.
	DownloadSubtitles(ctx context.Context, url string, subs []Subtitle, dir string) (map[string]string, error)
}

// videoPreference orders codec families for selection: VP9 first because
// YouTube offers it at every height and browsers decode it in hardware
// almost everywhere; H.264 as the universal fallback; AV1 last because
// hardware decoding is still uneven.
var videoPreference = []string{"vp09", "avc1", "av01"}

// SelectFormats picks one video codec family and the best format for each
// height in the ladder, plus the best audio track (Opus preferred). Heights
// the source does not offer are skipped; at least one must match.
func SelectFormats(p *Probe, ladder []int) (Selection, error) {
	byFamily := map[string][]Format{}
	for _, f := range p.Formats {
		if f.IsVideo() && !f.IsAudio() && f.Height > 0 {
			byFamily[f.CodecFamily()] = append(byFamily[f.CodecFamily()], f)
		}
	}

	var sel Selection
	for _, family := range videoPreference {
		formats := byFamily[family]
		if len(formats) == 0 {
			continue
		}
		video := pickLadder(formats, ladder)
		if len(video) == 0 {
			continue
		}
		sel.Video = video
		break
	}
	if len(sel.Video) == 0 {
		return Selection{}, fmt.Errorf("source: no video format matches the quality ladder %v", ladder)
	}

	audio, ok := pickAudio(p.Formats)
	if !ok {
		return Selection{}, errors.New("source: no audio-only format available")
	}
	sel.Audio = audio

	return sel, nil
}

// pickLadder returns the highest-bitrate format for each ladder height
// that the family offers, ordered by descending height.
func pickLadder(formats []Format, ladder []int) []Format {
	best := map[int]Format{}
	for _, f := range formats {
		if cur, ok := best[f.Height]; !ok || f.Bitrate > cur.Bitrate {
			best[f.Height] = f
		}
	}

	heights := append([]int(nil), ladder...)
	sort.Sort(sort.Reverse(sort.IntSlice(heights)))

	var out []Format
	seen := map[int]bool{}
	for _, h := range heights {
		if f, ok := best[h]; ok && !seen[h] {
			out = append(out, f)
			seen[h] = true
		}
	}
	return out
}

// audioPreference: Opus pairs with VP9 in WebM segments and is what
// browsers decode natively; AAC is the fallback.
var audioPreference = []string{"opus", "mp4a"}

func pickAudio(formats []Format) (Format, bool) {
	for _, family := range audioPreference {
		var best Format
		found := false
		for _, f := range formats {
			if f.IsAudio() && !f.IsVideo() && f.CodecFamily() == family && (!found || f.Bitrate > best.Bitrate) {
				best, found = f, true
			}
		}
		if found {
			return best, true
		}
	}
	return Format{}, false
}
