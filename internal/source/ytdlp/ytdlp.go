// Package ytdlp implements source.Extractor by shelling out to yt-dlp. It
// covers YouTube and, through yt-dlp's generic extractor, direct links and
// hundreds of other sites.
package ytdlp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/anesthetised/couchcast/internal/source"
)

// Extractor runs the yt-dlp binary.
type Extractor struct {
	Path      string   // yt-dlp binary
	ExtraArgs []string // appended to every invocation (cookies, PO token provider, ...)
	Logger    *slog.Logger
}

// New creates an extractor for the given binary path.
func New(path string, extraArgs []string, logger *slog.Logger) *Extractor {
	if logger == nil {
		logger = slog.Default()
	}
	return &Extractor{Path: path, ExtraArgs: extraArgs, Logger: logger}
}

var youtubeID = regexp.MustCompile(`(?:youtube\.com/(?:watch\?(?:.*&)?v=|shorts/|embed/|live/|v/)|youtu\.be/)([A-Za-z0-9_-]{11})`)

// Key implements source.Extractor. YouTube URLs dedupe on the video id so
// that every link form (watch, youtu.be, shorts, with playlist params)
// maps to one media row; everything else dedupes on the normalized URL.
func (e *Extractor) Key(raw string) (string, bool) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return "", false
	}

	if m := youtubeID.FindStringSubmatch(raw); m != nil {
		return "youtube:" + m[1], true
	}

	u.Scheme = strings.ToLower(u.Scheme)
	u.Host = strings.ToLower(u.Host)
	u.Fragment = ""
	return "url:" + u.String(), true
}

var youtubeList = regexp.MustCompile(`[?&]list=([A-Za-z0-9_-]{10,64})`)

// PlaylistURL implements source.Extractor for YouTube playlists. Mixes
// and "radio" lists (RD…) are generated per viewer and endless, so they
// are not offered.
func (e *Extractor) PlaylistURL(raw string) (string, bool, bool) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return "", false, false
	}
	host := strings.TrimPrefix(strings.ToLower(u.Host), "www.")
	host = strings.TrimPrefix(host, "m.")
	if host != "youtube.com" && host != "music.youtube.com" && host != "youtu.be" {
		return "", false, false
	}
	m := youtubeList.FindStringSubmatch(raw)
	if m == nil || strings.HasPrefix(m[1], "RD") {
		return "", false, false
	}
	return "https://www.youtube.com/playlist?list=" + m[1], youtubeID.MatchString(raw), true
}

// playlistJSON is the subset of yt-dlp's --flat-playlist -J output we read.
type playlistJSON struct {
	Title         string `json:"title"`
	Type          string `json:"_type"`
	PlaylistCount int    `json:"playlist_count"`
	Entries       []struct {
		ID         string  `json:"id"`
		URL        string  `json:"url"`
		Title      string  `json:"title"`
		Duration   float64 `json:"duration"`
		Thumbnails []struct {
			URL string `json:"url"`
		} `json:"thumbnails"`
	} `json:"entries"`
}

// Playlist implements source.Extractor with a flat listing: one request
// for the page, no per-video resolution.
func (e *Extractor) Playlist(ctx context.Context, rawURL string, limit int) (*source.Playlist, error) {
	args := append([]string{"-J", "--flat-playlist", "--no-warnings", "--playlist-end", strconv.Itoa(limit)}, e.ExtraArgs...)
	args = append(args, "--", rawURL)
	cmd := exec.CommandContext(ctx, e.Path, args...) //nolint:gosec // binary from config; url passed after "--"
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("yt-dlp playlist: %w: %s", err, lastLine(stderr.String()))
	}
	return ParsePlaylist(stdout.Bytes(), limit)
}

// ParsePlaylist converts flat yt-dlp JSON into a Playlist, dropping
// entries YouTube lists but will not play (private, deleted). Exported
// for tests against recorded fixtures.
func ParsePlaylist(data []byte, limit int) (*source.Playlist, error) {
	var pl playlistJSON
	if err := json.Unmarshal(data, &pl); err != nil {
		return nil, fmt.Errorf("yt-dlp: decode playlist: %w", err)
	}
	if pl.Type != "playlist" {
		return nil, errors.New("yt-dlp: not a playlist")
	}
	out := &source.Playlist{Title: pl.Title, Total: pl.PlaylistCount}
	for _, en := range pl.Entries {
		if len(out.Entries) == limit {
			break
		}
		if en.URL == "" || en.Title == "[Private video]" || en.Title == "[Deleted video]" {
			continue
		}
		entry := source.PlaylistEntry{URL: en.URL, Title: en.Title, DurationMs: int64(en.Duration * 1000)}
		if n := len(en.Thumbnails); n > 0 {
			entry.ThumbnailURL = en.Thumbnails[n-1].URL
		}
		out.Entries = append(out.Entries, entry)
	}
	if out.Total < len(out.Entries) {
		out.Total = len(out.Entries)
	}
	return out, nil
}

// infoJSON is the subset of yt-dlp's -J output we read.
type infoJSON struct {
	ID           string        `json:"id"`
	Title        string        `json:"title"`
	Duration     float64       `json:"duration"`
	Thumbnail    string        `json:"thumbnail"`
	ExtractorKey string        `json:"extractor_key"`
	Language     string        `json:"language"`
	Formats      []formatJSON  `json:"formats"`
	Chapters     []chapterJSON `json:"chapters"`
	// Subtitles are uploaded tracks; automatic captions are machine-made,
	// one per language the site offers (dozens on YouTube).
	Subtitles         map[string][]subtitleJSON `json:"subtitles"`
	AutomaticCaptions map[string][]subtitleJSON `json:"automatic_captions"`
}

type chapterJSON struct {
	StartTime float64 `json:"start_time"`
	EndTime   float64 `json:"end_time"`
	Title     string  `json:"title"`
}

type subtitleJSON struct {
	Ext  string `json:"ext"`
	Name string `json:"name"`
}

// maxSubtitleTracks bounds how many languages are packaged per video.
const maxSubtitleTracks = 10

type formatJSON struct {
	FormatID       string  `json:"format_id"`
	Ext            string  `json:"ext"`
	VCodec         string  `json:"vcodec"`
	ACodec         string  `json:"acodec"`
	Height         int     `json:"height"`
	Width          int     `json:"width"`
	TBR            float64 `json:"tbr"`
	Filesize       int64   `json:"filesize"`
	FilesizeApprox int64   `json:"filesize_approx"`
	Protocol       string  `json:"protocol"`
	FormatNote     string  `json:"format_note"`
}

// Probe implements source.Extractor.
func (e *Extractor) Probe(ctx context.Context, rawURL string) (*source.Probe, error) {
	args := append([]string{"-J", "--no-playlist", "--no-warnings"}, e.ExtraArgs...)
	args = append(args, "--", rawURL)

	cmd := exec.CommandContext(ctx, e.Path, args...) //nolint:gosec // binary from config; url passed after "--"
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("yt-dlp probe: %w: %s", err, lastLine(stderr.String()))
	}

	return ParseInfo(stdout.Bytes())
}

// ParseInfo converts yt-dlp JSON into a Probe. Exported for tests against
// recorded fixtures.
func ParseInfo(data []byte) (*source.Probe, error) {
	var info infoJSON
	if err := json.Unmarshal(data, &info); err != nil {
		return nil, fmt.Errorf("yt-dlp: decode info: %w", err)
	}

	p := &source.Probe{
		Title:        info.Title,
		DurationMs:   int64(info.Duration * 1000),
		ThumbnailURL: info.Thumbnail,
	}

	for _, f := range info.Formats {
		if !usable(f) {
			continue
		}
		size := f.Filesize
		if size == 0 {
			size = f.FilesizeApprox
		}
		p.Formats = append(p.Formats, source.Format{
			ID:       f.FormatID,
			Ext:      f.Ext,
			VCodec:   noneToEmpty(f.VCodec),
			ACodec:   noneToEmpty(f.ACodec),
			Height:   f.Height,
			Width:    f.Width,
			Bitrate:  int(f.TBR),
			Filesize: size,
		})
	}

	if len(p.Formats) == 0 {
		return nil, errors.New("yt-dlp: no downloadable formats")
	}

	p.Subtitles = pickSubtitles(info)
	p.Chapters = chapters(info)
	return p, nil
}

// maxChapters bounds the list; a few hundred is already a table of
// contents nobody scrolls.
const maxChapters = 200

// chapters keeps well-formed, ordered entries; a single chapter spanning
// the whole video says nothing and is dropped.
func chapters(info infoJSON) []source.Chapter {
	out := make([]source.Chapter, 0, len(info.Chapters))
	var last int64 = -1
	for _, c := range info.Chapters {
		start, end := int64(c.StartTime*1000), int64(c.EndTime*1000)
		title := strings.TrimSpace(c.Title)
		if title == "" || start < last || end <= start {
			continue
		}
		out = append(out, source.Chapter{StartMs: start, EndMs: end, Title: title})
		last = start
		if len(out) == maxChapters {
			break
		}
	}
	if len(out) < 2 {
		return nil
	}
	return out
}

// pickSubtitles takes every uploaded track (up to the cap, in language
// order) and, when there are none, the automatic captions in the video's
// own language only — the translated ones are noise.
func pickSubtitles(info infoJSON) []source.Subtitle {
	var out []source.Subtitle
	langs := make([]string, 0, len(info.Subtitles))
	for lang, tracks := range info.Subtitles {
		if len(tracks) > 0 && !strings.HasSuffix(lang, "-live_chat") {
			langs = append(langs, lang)
		}
	}
	sort.Strings(langs)
	for _, lang := range langs {
		if len(out) >= maxSubtitleTracks {
			break
		}
		out = append(out, source.Subtitle{Lang: lang, Name: info.Subtitles[lang][0].Name})
	}
	if len(out) > 0 {
		return out
	}
	for _, lang := range []string{info.Language, info.Language + "-orig"} {
		if lang == "" || lang == "-orig" {
			continue
		}
		if tracks, ok := info.AutomaticCaptions[lang]; ok && len(tracks) > 0 {
			return []source.Subtitle{{Lang: info.Language, Name: tracks[0].Name, Auto: true}}
		}
	}
	return nil
}

// DownloadSubtitles implements source.Extractor with a separate yt-dlp run
// (no media download), so the progress parser of Download stays simple.
func (e *Extractor) DownloadSubtitles(ctx context.Context, rawURL string, subs []source.Subtitle, dir string) (map[string]string, error) {
	if len(subs) == 0 {
		return map[string]string{}, nil
	}
	langs := make([]string, 0, len(subs))
	auto := false
	for _, s := range subs {
		langs = append(langs, s.Lang)
		auto = auto || s.Auto
	}
	args := []string{"--no-playlist", "--no-warnings", "--skip-download", "--write-subs",
		"--sub-langs", strings.Join(langs, ","), "--sub-format", "vtt/best", "--convert-subs", "vtt",
		"-o", filepath.Join(dir, "subs.%(ext)s")}
	if auto {
		args = append(args, "--write-auto-subs")
	}
	args = append(args, e.ExtraArgs...)
	args = append(args, "--", rawURL)

	cmd := exec.CommandContext(ctx, e.Path, args...) //nolint:gosec // binary from config; url passed after "--"
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("yt-dlp subtitles: %w: %s", err, lastLine(stderr.String()))
	}

	files := make(map[string]string, len(subs))
	for _, s := range subs {
		// yt-dlp names the file subs.<lang>.vtt; the auto track may come as
		// <lang>-orig or with a different case.
		for _, cand := range []string{s.Lang, s.Lang + "-orig", strings.ToLower(s.Lang)} {
			if p := filepath.Join(dir, "subs."+cand+".vtt"); fileExists(p) {
				files[s.Lang] = p
				break
			}
		}
	}
	return files, nil
}

func fileExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}

// usable filters out formats that cannot be downloaded as a plain file or
// that duplicate another with post-processing (DRC audio, storyboards).
func usable(f formatJSON) bool {
	switch f.Protocol {
	case "https", "http", "":
	default:
		return false
	}
	if strings.HasSuffix(f.FormatID, "-drc") || strings.Contains(f.FormatNote, "Premium") {
		return false
	}
	if noneToEmpty(f.VCodec) == "" && noneToEmpty(f.ACodec) == "" {
		return false
	}
	return true
}

func noneToEmpty(s string) string {
	if s == "none" {
		return ""
	}
	return s
}

// progressRe matches the --progress-template line emitted per download.
var progressRe = regexp.MustCompile(`^cc-progress:(\d+)/(\d+)$`)

// Download implements source.Extractor. Formats are fetched one after
// another (yt-dlp does this for a comma-separated -f) into dir named by
// format id.
func (e *Extractor) Download(ctx context.Context, rawURL string, formats []source.Format, dir string, progress func(float64)) (map[string]string, error) {
	formatIDs := make([]string, 0, len(formats))
	sizes := make([]int64, 0, len(formats))
	for _, f := range formats {
		formatIDs = append(formatIDs, f.ID)
		sizes = append(sizes, f.Filesize)
	}

	args := make([]string, 0, 12+len(e.ExtraArgs))
	args = append(args,
		"--no-playlist", "--no-warnings", "--newline", "--no-part",
		"--progress-template", "download:cc-progress:%(progress.downloaded_bytes)s/%(progress.total_bytes,progress.total_bytes_estimate|0)s",
		"-f", strings.Join(formatIDs, ","),
		"-o", filepath.Join(dir, "%(format_id)s.%(ext)s"),
	)
	args = append(args, e.ExtraArgs...)
	args = append(args, "--", rawURL)

	cmd := exec.CommandContext(ctx, e.Path, args...) //nolint:gosec // binary from config; url passed after "--"
	// Python buffers stdout when it is a pipe, which would batch the
	// progress lines into 8 KB bursts.
	cmd.Env = append(os.Environ(), "PYTHONUNBUFFERED=1")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("yt-dlp download: %w", err)
	}

	tracker := newProgressTracker(sizes, progress)
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	for scanner.Scan() {
		tracker.line(scanner.Text())
	}

	if err := cmd.Wait(); err != nil {
		return nil, fmt.Errorf("yt-dlp download: %w: %s", err, lastLine(stderr.String()))
	}

	files := make(map[string]string, len(formatIDs))
	for _, id := range formatIDs {
		matches, _ := filepath.Glob(filepath.Join(dir, id+".*"))
		if len(matches) == 0 {
			return nil, fmt.Errorf("yt-dlp download: format %s produced no file", id)
		}
		files[id] = matches[0]
	}

	return files, nil
}

// progressTracker turns per-file byte counts into one 0..1 value for the
// batch, weighting each file by its expected size so the largest rendition
// does not look stuck at "20%". yt-dlp downloads formats sequentially, so
// a new file is detected when downloaded bytes drop. Files with unknown
// size get the average weight.
type progressTracker struct {
	weights  []float64 // normalised, sum to 1
	index    int
	lastSeen int64
	report   func(float64)
}

func newProgressTracker(sizes []int64, report func(float64)) *progressTracker {
	if report == nil {
		report = func(float64) {}
	}
	n := len(sizes)
	weights := make([]float64, n)
	if n == 0 {
		return &progressTracker{weights: weights, report: report}
	}

	var known, sum float64
	for _, s := range sizes {
		if s > 0 {
			known++
			sum += float64(s)
		}
	}
	avg := 1.0
	if known > 0 {
		avg = sum / known
	}
	total := 0.0
	for i, s := range sizes {
		w := float64(s)
		if s <= 0 {
			w = avg
		}
		weights[i] = w
		total += w
	}
	for i := range weights {
		weights[i] /= total
	}
	return &progressTracker{weights: weights, report: report}
}

func (t *progressTracker) line(s string) {
	m := progressRe.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil || len(t.weights) == 0 {
		return
	}
	downloaded, _ := strconv.ParseInt(m[1], 10, 64)
	size, _ := strconv.ParseInt(m[2], 10, 64)

	if downloaded < t.lastSeen && t.index < len(t.weights)-1 {
		t.index++
	}
	t.lastSeen = downloaded

	frac := 0.0
	if size > 0 {
		frac = min(float64(downloaded)/float64(size), 1)
	}

	done := 0.0
	for i := 0; i < t.index; i++ {
		done += t.weights[i]
	}
	t.report(done + frac*t.weights[t.index])
}

func lastLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.LastIndexByte(s, '\n'); i >= 0 {
		s = s[i+1:]
	}
	return s
}
