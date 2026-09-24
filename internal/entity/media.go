package entity

import (
	"time"

	"uuid"
)

// MediaStatus is the ingest lifecycle of a media item.
type MediaStatus string

const (
	MediaQueued      MediaStatus = "queued"
	MediaProbing     MediaStatus = "probing"
	MediaDownloading MediaStatus = "downloading"
	MediaPackaging   MediaStatus = "packaging"
	MediaUploading   MediaStatus = "uploading"
	MediaReady       MediaStatus = "ready"
	MediaFailed      MediaStatus = "failed"
)

// Rendition describes one video quality inside the DASH manifest.
type Rendition struct {
	ID      string `json:"id"`     // DASH representation id
	Height  int    `json:"height"` // 1080, 720, ...
	Width   int    `json:"width"`
	Codec   string `json:"codec"`   // vp09, avc1, av01
	Bitrate int    `json:"bitrate"` // kbit/s
}

// Subtitle is one text track packaged with the media (sub-<lang>.vtt).
type Subtitle struct {
	Lang string `json:"lang"`
	Name string `json:"name"`
	Auto bool   `json:"auto,omitempty"` // machine-generated captions
}

// Chapter is a named section of the video as the source reports it.
type Chapter struct {
	StartMs int64  `json:"startMs"`
	EndMs   int64  `json:"endMs"`
	Title   string `json:"title"`
}

// Media is a video that has been (or is being) ingested. It is shared by
// every room that queues the same source, keyed by SourceKey.
type Media struct {
	ID             uuid.UUID
	SourceKey      string
	SourceURL      string
	Title          string
	DurationMs     int64
	ThumbnailURL   string
	Status         MediaStatus
	Progress       float32 // 0..1 within the current step
	SpeedBps       int64   // bytes per second while downloading, 0 when unknown
	EtaMs          int64   // time left in the current step, 0 when unknown
	Error          string
	SizeBytes      int64
	Renditions     []Rendition
	Subtitles      []Subtitle
	Chapters       []Chapter
	Storyboard     *Storyboard
	S3Prefix       string
	CreatedAt      time.Time
	UpdatedAt      time.Time
	LastAccessedAt time.Time
}

// IsReady reports whether the DASH output is available.
func (m *Media) IsReady() bool { return m != nil && m.Status == MediaReady }

// InProgress reports whether the ingest pipeline is still working on it.
func (m *Media) InProgress() bool {
	switch m.Status {
	case MediaQueued, MediaProbing, MediaDownloading, MediaPackaging, MediaUploading:
		return true
	default:
		return false
	}
}

// Storyboard describes the timeline preview sheets stored next to the
// DASH output (sb-0.jpg, sb-1.jpg, …): frame i shows the video at
// i×IntervalMs and sits in sheet i/(Cols×Rows), cell i%(Cols×Rows).
type Storyboard struct {
	IntervalMs int64 `json:"intervalMs"`
	Width      int   `json:"width"`
	Height     int   `json:"height"`
	Cols       int   `json:"cols"`
	Rows       int   `json:"rows"`
	Count      int   `json:"count"`
	Sheets     int   `json:"sheets"`
}
