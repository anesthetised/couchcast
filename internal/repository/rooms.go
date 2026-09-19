package repository

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"uuid"

	"github.com/jackc/pgx/v5"

	"github.com/anesthetised/couchcast/internal/entity"
)

const roomColumns = `id, slug, name, owner_id, visibility, settings, current_item_id, playing, position_ms, position_at, created_at, updated_at`

func scanRoom(row pgx.Row) (*entity.Room, error) {
	var (
		r        entity.Room
		settings []byte
	)
	err := row.Scan(&r.ID, &r.Slug, &r.Name, &r.OwnerID, &r.Visibility, &settings,
		&r.CurrentItemID, &r.Playing, &r.PositionMs, &r.PositionAt, &r.CreatedAt, &r.UpdatedAt)
	if err != nil {
		return nil, wrapErr(err)
	}

	r.Settings = entity.DefaultSettings()
	if len(settings) > 0 {
		if err := json.Unmarshal(settings, &r.Settings); err != nil {
			return nil, fmt.Errorf("room %s: decode settings: %w", r.ID, err)
		}
	}

	return &r, nil
}

// CreateRoom inserts the room and its owner membership in one transaction.
// Returns ErrConflict when the slug is taken.
func (r *Repo) CreateRoom(ctx context.Context, slug, name string, ownerID uuid.UUID, visibility entity.Visibility, settings entity.Settings) (*entity.Room, error) {
	settingsJSON, err := json.Marshal(settings)
	if err != nil {
		return nil, err
	}

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after commit

	const insertRoom = `
		INSERT INTO rooms (slug, name, owner_id, visibility, settings)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING ` + roomColumns

	room, err := scanRoom(tx.QueryRow(ctx, insertRoom, slug, name, ownerID, visibility, settingsJSON))
	if err != nil {
		return nil, err
	}

	const insertOwner = `INSERT INTO room_members (room_id, user_id, role) VALUES ($1, $2, 'owner')`
	if _, err := tx.Exec(ctx, insertOwner, room.ID, ownerID); err != nil {
		return nil, wrapErr(err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}

	return room, nil
}

// GetRoomBySlug returns a room or ErrNotFound.
func (r *Repo) GetRoomBySlug(ctx context.Context, slug string) (*entity.Room, error) {
	const q = `SELECT ` + roomColumns + ` FROM rooms WHERE slug = $1`
	return scanRoom(r.pool.QueryRow(ctx, q, slug))
}

// GetRoomByID returns a room or ErrNotFound.
func (r *Repo) GetRoomByID(ctx context.Context, id uuid.UUID) (*entity.Room, error) {
	const q = `SELECT ` + roomColumns + ` FROM rooms WHERE id = $1`
	return scanRoom(r.pool.QueryRow(ctx, q, id))
}

// UpdateRoom changes the fields an owner may edit.
func (r *Repo) UpdateRoom(ctx context.Context, id uuid.UUID, slug, name string, visibility entity.Visibility) (*entity.Room, error) {
	const q = `
		UPDATE rooms SET slug = $2, name = $3, visibility = $4, updated_at = now()
		WHERE id = $1
		RETURNING ` + roomColumns
	return scanRoom(r.pool.QueryRow(ctx, q, id, slug, name, visibility))
}

// UpdateRoomSettings replaces the settings document.
func (r *Repo) UpdateRoomSettings(ctx context.Context, id uuid.UUID, settings entity.Settings) error {
	b, err := json.Marshal(settings)
	if err != nil {
		return err
	}
	const q = `UPDATE rooms SET settings = $2, updated_at = now() WHERE id = $1`
	return r.exec(ctx, q, id, b)
}

// DeleteRoom removes the room; memberships, bans, invites, queue and chat
// cascade in the database.
func (r *Repo) DeleteRoom(ctx context.Context, id uuid.UUID) error {
	const q = `DELETE FROM rooms WHERE id = $1`
	return r.exec(ctx, q, id)
}

// RoomWithRole is a room paired with the querying user's role in it.
type RoomWithRole struct {
	Room entity.Room
	Role entity.RoomRole
}

// ListRoomsForUser returns every room the user is a member of, newest
// membership first.
func (r *Repo) ListRoomsForUser(ctx context.Context, userID uuid.UUID) ([]RoomWithRole, error) {
	const q = `
		SELECT r.id, r.slug, r.name, r.owner_id, r.visibility, r.settings, r.current_item_id,
		       r.playing, r.position_ms, r.position_at, r.created_at, r.updated_at, m.role
		FROM room_members m
		JOIN rooms r ON r.id = m.room_id
		WHERE m.user_id = $1
		ORDER BY m.joined_at DESC
	`
	rows, err := r.pool.Query(ctx, q, userID)
	if err != nil {
		return nil, wrapErr(err)
	}
	defer rows.Close()

	var out []RoomWithRole
	for rows.Next() {
		var (
			item     RoomWithRole
			settings []byte
		)
		rm := &item.Room
		if err := rows.Scan(&rm.ID, &rm.Slug, &rm.Name, &rm.OwnerID, &rm.Visibility, &settings, &rm.CurrentItemID,
			&rm.Playing, &rm.PositionMs, &rm.PositionAt, &rm.CreatedAt, &rm.UpdatedAt, &item.Role); err != nil {
			return nil, err
		}
		rm.Settings = entity.DefaultSettings()
		if len(settings) > 0 {
			if err := json.Unmarshal(settings, &rm.Settings); err != nil {
				return nil, err
			}
		}
		out = append(out, item)
	}

	return out, rows.Err()
}

// CountMembers returns the number of membership rows in a room.
func (r *Repo) CountMembers(ctx context.Context, roomID uuid.UUID) (int, error) {
	const q = `SELECT count(*) FROM room_members WHERE room_id = $1`
	var n int
	err := r.pool.QueryRow(ctx, q, roomID).Scan(&n)
	return n, wrapErr(err)
}

// PublicRoom is a directory entry: the room, its current media (nil when
// nothing is queued) and counts.
type PublicRoom struct {
	Room        entity.Room
	Owner       string
	Media       *entity.Media
	MemberCount int
	Viewers     int
}

// PublicRoomsQuery selects and pages the public directory. Live viewer
// counts come from the in-memory room manager and are passed in so that
// filtering, ordering and pagination all happen in one SQL query.
type PublicRoomsQuery struct {
	Search     string
	LiveIDs    []uuid.UUID
	LiveCounts []int
	OnlyLive   bool
	Offset     int
	Limit      int
}

// ListPublicRooms returns one page of public rooms (live rooms first, then
// rooms with something playing, then by recency) and the total match count.
func (r *Repo) ListPublicRooms(ctx context.Context, q PublicRoomsQuery) ([]PublicRoom, int, error) {
	if q.LiveIDs == nil {
		q.LiveIDs = []uuid.UUID{}
	}
	if q.LiveCounts == nil {
		q.LiveCounts = []int{}
	}

	const sql = `
		SELECT r.id, r.slug, r.name, r.owner_id, r.visibility, r.settings, r.current_item_id,
		       r.playing, r.position_ms, r.position_at, r.created_at, r.updated_at,
		       u.username,
		       (SELECT count(*) FROM room_members m WHERE m.room_id = r.id),
		       coalesce(v.viewers, 0),
		       m.id, coalesce(m.source_key, ''), coalesce(m.source_url, ''), coalesce(m.title, ''), coalesce(m.duration_ms, 0), coalesce(m.thumbnail_url, ''),
		       m.status, m.progress, coalesce(m.error, ''), coalesce(m.size_bytes, 0), m.renditions, coalesce(m.s3_prefix, ''),
		       m.created_at, m.updated_at, m.last_accessed_at,
		       count(*) OVER()
		FROM rooms r
		JOIN users u ON u.id = r.owner_id
		LEFT JOIN unnest($1::uuid[], $2::int[]) AS v(id, viewers) ON v.id = r.id
		LEFT JOIN queue_items qi ON qi.id = r.current_item_id
		LEFT JOIN media m ON m.id = qi.media_id
		WHERE r.visibility = 'public'
		  AND ($3 = '' OR r.name ILIKE '%' || $3 || '%' ESCAPE '\' OR r.slug ILIKE '%' || $3 || '%' ESCAPE '\')
		  AND (NOT $4 OR v.id IS NOT NULL)
		ORDER BY coalesce(v.viewers, 0) DESC, (r.current_item_id IS NOT NULL) DESC, r.updated_at DESC
		OFFSET $5 LIMIT $6
	`
	rows, err := r.pool.Query(ctx, sql, q.LiveIDs, q.LiveCounts, escapeLike(q.Search), q.OnlyLive, q.Offset, q.Limit)
	if err != nil {
		return nil, 0, wrapErr(err)
	}
	defer rows.Close()

	var (
		out   []PublicRoom
		total int
	)
	for rows.Next() {
		var (
			pr                            PublicRoom
			settings                      []byte
			mediaID                       *uuid.UUID
			m                             entity.Media
			mStatus                       *entity.MediaStatus
			mProg                         *float32
			mRend                         []byte
			mCreated, mUpdated, mAccessed *time.Time
		)
		rm := &pr.Room
		if err := rows.Scan(&rm.ID, &rm.Slug, &rm.Name, &rm.OwnerID, &rm.Visibility, &settings, &rm.CurrentItemID,
			&rm.Playing, &rm.PositionMs, &rm.PositionAt, &rm.CreatedAt, &rm.UpdatedAt,
			&pr.Owner, &pr.MemberCount, &pr.Viewers,
			&mediaID, &m.SourceKey, &m.SourceURL, &m.Title, &m.DurationMs, &m.ThumbnailURL,
			&mStatus, &mProg, &m.Error, &m.SizeBytes, &mRend, &m.S3Prefix,
			&mCreated, &mUpdated, &mAccessed,
			&total); err != nil {
			return nil, 0, err
		}
		rm.Settings = entity.DefaultSettings()
		if len(settings) > 0 {
			if err := json.Unmarshal(settings, &rm.Settings); err != nil {
				return nil, 0, err
			}
		}
		if mediaID != nil {
			m.ID = *mediaID
			m.Status = *mStatus
			m.Progress = *mProg
			m.CreatedAt, m.UpdatedAt, m.LastAccessedAt = *mCreated, *mUpdated, *mAccessed
			if len(mRend) > 0 {
				_ = json.Unmarshal(mRend, &m.Renditions)
			}
			media := m
			pr.Media = &media
		}
		out = append(out, pr)
	}
	return out, total, rows.Err()
}

// escapeLike makes user input literal inside an ILIKE pattern.
func escapeLike(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `%`, `\%`)
	return strings.ReplaceAll(s, `_`, `\_`)
}
