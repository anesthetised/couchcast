package entity

import (
	"encoding/json"
	"time"

	"uuid"
)

// ReportReason categorises a media report.
type ReportReason string

const (
	ReportCopyright ReportReason = "copyright"
	ReportIllegal   ReportReason = "illegal"
	ReportNSFW      ReportReason = "nsfw"
	ReportOther     ReportReason = "other"
)

// ValidReportReason reports whether r is one of the known reasons.
func ValidReportReason(r ReportReason) bool {
	switch r {
	case ReportCopyright, ReportIllegal, ReportNSFW, ReportOther:
		return true
	}
	return false
}

// MediaReport is one user's complaint about a media item.
type MediaReport struct {
	ID         uuid.UUID
	MediaID    uuid.UUID
	ReporterID uuid.UUID
	Reporter   string // username, populated by queries
	RoomID     *uuid.UUID
	RoomSlug   string // populated by queries; empty when the room is gone
	RoomName   string
	AddedBy    string // username of whoever queued it there, if known
	Reason     ReportReason
	Comment    string
	CreatedAt  time.Time
	ResolvedAt *time.Time
}

// Placement is a room whose queue currently holds a media item.
type Placement struct {
	RoomID   uuid.UUID
	RoomSlug string
	RoomName string
	AddedBy  string
}

// ReportedMedia aggregates open reports per media for the admin panel.
type ReportedMedia struct {
	Media      Media
	Count      int
	Reports    []MediaReport
	Placements []Placement
}

// BlocklistEntry is a source key administrators refuse to ingest.
type BlocklistEntry struct {
	SourceKey string
	Reason    string
	CreatedBy string // username, may be empty
	CreatedAt time.Time
}

// Stats is the admin dashboard summary.
type Stats struct {
	Users         int
	BannedUsers   int
	Rooms         int
	PrivateRooms  int
	MediaByStatus map[string]int
	MediaBytes    int64
	PendingJobs   int
	RunningJobs   int
	FailedJobs    int
	OpenReports   int
}

// AdminUser is a user row for the admin list.
type AdminUser struct {
	User      User
	RoomCount int
}

// BugCategory is what a problem report is about.
type BugCategory string

const (
	BugPlayback  BugCategory = "playback"
	BugSync      BugCategory = "sync"
	BugSubtitles BugCategory = "subtitles"
	BugChat      BugCategory = "chat"
	BugOther     BugCategory = "other"
)

// Valid reports whether the category is known.
func (c BugCategory) Valid() bool {
	switch c {
	case BugPlayback, BugSync, BugSubtitles, BugChat, BugOther:
		return true
	}
	return false
}

// BugReport is a viewer's problem report with the diagnostics both sides
// collected. Client and Server are opaque JSON; Frame is a JPEG.
type BugReport struct {
	ID          uuid.UUID
	UserID      *uuid.UUID
	Username    string // populated by queries
	RoomID      *uuid.UUID
	RoomSlug    string // populated by queries
	MediaID     *uuid.UUID
	MediaTitle  string // populated by queries
	Category    BugCategory
	Description string
	Client      json.RawMessage
	Server      json.RawMessage
	HasFrame    bool
	CreatedAt   time.Time
	ResolvedAt  *time.Time
	ResolvedBy  *uuid.UUID
	Note        string
}
