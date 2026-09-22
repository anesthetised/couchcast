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

const roomColumns = `id, slug, name, owner_id, visibility, description, settings, current_item_id, playing, position_ms, position_at, rate, pinned_message_id, created_at, updated_at`

func scanRoom(row pgx.Row) (*entity.Room, error) {
	var (
		r        entity.Room
		settings []byte
	)
	err := row.Scan(&r.ID, &r.Slug, &r.Name, &r.OwnerID, &r.Visibility, &r.Description, &settings,
		&r.CurrentItemID, &r.Playing, &r.PositionMs, &r.PositionAt, &r.Rate, &r.PinnedMessageID, &r.CreatedAt, &r.UpdatedAt)
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

	// A new room claims the slug from whichever room used to have it.
	if _, err := tx.Exec(ctx, `DELETE FROM room_slug_history WHERE slug = $1`, slug); err != nil {
		return nil, wrapErr(err)
	}

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

// GetRoomBySlug returns a room or ErrNotFound. A former slug resolves to
// the room that used to have it; the caller sees the current slug on the
// returned room and can redirect.
func (r *Repo) GetRoomBySlug(ctx context.Context, slug string) (*entity.Room, error) {
	const q = `
		SELECT ` + roomColumns + ` FROM rooms
		WHERE slug = $1 OR id = (SELECT room_id FROM room_slug_history WHERE slug = $1)
		ORDER BY slug = $1 DESC
		LIMIT 1
	`
	return scanRoom(r.pool.QueryRow(ctx, q, slug))
}

// GetRoomByID returns a room or ErrNotFound.
func (r *Repo) GetRoomByID(ctx context.Context, id uuid.UUID) (*entity.Room, error) {
	const q = `SELECT ` + roomColumns + ` FROM rooms WHERE id = $1`
	return scanRoom(r.pool.QueryRow(ctx, q, id))
}

// UpdateRoom changes the fields an owner may edit. A slug change keeps
// the old slug in the history and reclaims the new one from it.
func (r *Repo) UpdateRoom(ctx context.Context, id uuid.UUID, slug, name string, visibility entity.Visibility, description string) (*entity.Room, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after commit

	const remember = `
		INSERT INTO room_slug_history (slug, room_id)
		SELECT slug, id FROM rooms WHERE id = $1 AND slug <> $2
		ON CONFLICT (slug) DO UPDATE SET room_id = EXCLUDED.room_id, created_at = now()
	`
	if _, err := tx.Exec(ctx, remember, id, slug); err != nil {
		return nil, wrapErr(err)
	}
	if _, err := tx.Exec(ctx, `DELETE FROM room_slug_history WHERE slug = $1`, slug); err != nil {
		return nil, wrapErr(err)
	}

	const q = `
		UPDATE rooms SET slug = $2, name = $3, visibility = $4, description = $5, updated_at = now()
		WHERE id = $1
		RETURNING ` + roomColumns
	room, err := scanRoom(tx.QueryRow(ctx, q, id, slug, name, visibility, description))
	if err != nil {
		return nil, err
	}
	return room, tx.Commit(ctx)
}

// GetCurrentMedia returns the media the room is on, or ErrNotFound when
// nothing is current.
func (r *Repo) GetCurrentMedia(ctx context.Context, roomID uuid.UUID) (*entity.Media, error) {
	const q = `
		SELECT ` + mediaColumns + ` FROM media
		WHERE id = (SELECT qi.media_id FROM rooms r JOIN queue_items qi ON qi.id = r.current_item_id WHERE r.id = $1)
	`
	return scanMedia(r.pool.QueryRow(ctx, q, roomID))
}

// TransferOwnership makes a member the owner and demotes the previous
// owner to moderator; ErrNotFound when the target is not a member.
func (r *Repo) TransferOwnership(ctx context.Context, roomID, from, to uuid.UUID) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after commit

	const promote = `UPDATE room_members SET role = 'owner' WHERE room_id = $1 AND user_id = $2`
	tag, err := tx.Exec(ctx, promote, roomID, to)
	if err != nil {
		return wrapErr(err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	const demote = `UPDATE room_members SET role = 'moderator' WHERE room_id = $1 AND user_id = $2`
	if _, err := tx.Exec(ctx, demote, roomID, from); err != nil {
		return wrapErr(err)
	}
	const owner = `UPDATE rooms SET owner_id = $2, updated_at = now() WHERE id = $1`
	if _, err := tx.Exec(ctx, owner, roomID, to); err != nil {
		return wrapErr(err)
	}
	return tx.Commit(ctx)
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
		SELECT r.id, r.slug, r.name, r.owner_id, r.visibility, r.description, r.settings, r.current_item_id,
		       r.playing, r.position_ms, r.position_at, r.rate, r.created_at, r.updated_at, m.role
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
		if err := rows.Scan(&rm.ID, &rm.Slug, &rm.Name, &rm.OwnerID, &rm.Visibility, &rm.Description, &settings, &rm.CurrentItemID,
			&rm.Playing, &rm.PositionMs, &rm.PositionAt, &rm.Rate, &rm.CreatedAt, &rm.UpdatedAt, &item.Role); err != nil {
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

// ListPlayingRoomIDs returns rooms whose persisted state is "playing".
func (r *Repo) ListPlayingRoomIDs(ctx context.Context) ([]uuid.UUID, error) {
	const q = `SELECT id FROM rooms WHERE playing AND current_item_id IS NOT NULL`
	rows, err := r.pool.Query(ctx, q)
	if err != nil {
		return nil, wrapErr(err)
	}
	defer rows.Close()

	var out []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
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

// SetStar adds or removes the user's star on a room.
func (r *Repo) SetStar(ctx context.Context, userID, roomID uuid.UUID, on bool) error {
	if on {
		const q = `INSERT INTO room_stars (user_id, room_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`
		_, err := r.pool.Exec(ctx, q, userID, roomID)
		return wrapErr(err)
	}
	const q = `DELETE FROM room_stars WHERE user_id = $1 AND room_id = $2`
	_, err := r.pool.Exec(ctx, q, userID, roomID)
	return wrapErr(err)
}

// IsStarred reports whether the user starred the room.
func (r *Repo) IsStarred(ctx context.Context, userID, roomID uuid.UUID) (bool, error) {
	const q = `SELECT EXISTS (SELECT 1 FROM room_stars WHERE user_id = $1 AND room_id = $2)`
	var on bool
	if err := r.pool.QueryRow(ctx, q, userID, roomID).Scan(&on); err != nil {
		return false, wrapErr(err)
	}
	return on, nil
}

// DirectoryRoom is a directory entry: the room, its current media (nil
// when nothing is queued), counts and the viewer's own role.
type DirectoryRoom struct {
	Room        entity.Room
	Owner       string
	Media       *entity.Media
	MemberCount int
	Viewers     int
	Live        bool            // a ready video is playing right now
	MyRole      entity.RoomRole // "" when the viewer is not a member
	Starred     bool            // by the viewer
}

// DirectoryQuery selects and pages the room directory. Public rooms are
// always listed; private rooms only when ViewerID is a member; rooms the
// viewer is banned from never. A room is live when a ready video is
// playing in it, whoever is watching. Viewer counts come from the
// in-memory room manager and are passed in so ordering and pagination
// happen in one SQL query.
type DirectoryQuery struct {
	ViewerID    *uuid.UUID
	Search      string
	LiveIDs     []uuid.UUID
	LiveCounts  []int
	OnlyLive    bool
	OnlyPrivate bool // requires ViewerID
	OnlyMine    bool // requires ViewerID
	OnlyStarred bool // requires ViewerID
	Sort        DirectorySort
	Offset      int
	Limit       int
}

// DirectorySort orders the directory; the zero value is "active".
type DirectorySort string

const (
	SortActive  DirectorySort = "active"  // live, then viewers, then queued, then recency
	SortViewers DirectorySort = "viewers" // most watched first
	SortNewest  DirectorySort = "newest"  // recently created first
	SortName    DirectorySort = "name"    // alphabetical
)

// ParseDirectorySort maps a query value to a sort; unknown values fall
// back to SortActive.
func ParseDirectorySort(s string) DirectorySort {
	switch DirectorySort(s) {
	case SortViewers, SortNewest, SortName:
		return DirectorySort(s)
	default:
		return SortActive
	}
}

// orderBy is the ORDER BY clause for a sort; the strings are fixed here,
// never taken from the request.
func (s DirectorySort) orderBy() string {
	switch s {
	case SortViewers:
		return "coalesce(v.viewers, 0) DESC, (r.playing AND m.status = 'ready') DESC, r.updated_at DESC"
	case SortNewest:
		return "r.created_at DESC"
	case SortName:
		return "lower(r.name), r.created_at"
	default:
		return "(r.playing AND m.status = 'ready') DESC, coalesce(v.viewers, 0) DESC, (r.current_item_id IS NOT NULL) DESC, r.updated_at DESC"
	}
}

// ListDirectory returns one page of rooms visible to the viewer (live
// first, then by viewers, then rooms with something queued, then by
// recency) and the total match count.
func (r *Repo) ListDirectory(ctx context.Context, q DirectoryQuery) ([]DirectoryRoom, int, error) {
	if q.LiveIDs == nil {
		q.LiveIDs = []uuid.UUID{}
	}
	if q.LiveCounts == nil {
		q.LiveCounts = []int{}
	}
	if q.ViewerID == nil {
		q.OnlyPrivate, q.OnlyMine, q.OnlyStarred = false, false, false
	}

	sql := `
		SELECT r.id, r.slug, r.name, r.owner_id, r.visibility, r.settings, r.current_item_id,
		       r.playing, r.position_ms, r.position_at, r.rate, r.created_at, r.updated_at,
		       u.username,
		       (SELECT count(*) FROM room_members m WHERE m.room_id = r.id),
		       coalesce(v.viewers, 0),
		       (r.playing AND m.status = 'ready') AS live,
		       coalesce(me.role, ''),
		       st.user_id IS NOT NULL,
		       m.id, coalesce(m.source_key, ''), coalesce(m.source_url, ''), coalesce(m.title, ''), coalesce(m.duration_ms, 0), coalesce(m.thumbnail_url, ''),
		       m.status, m.progress, coalesce(m.error, ''), coalesce(m.size_bytes, 0), m.renditions, coalesce(m.s3_prefix, ''),
		       m.created_at, m.updated_at, m.last_accessed_at,
		       count(*) OVER()
		FROM rooms r
		JOIN users u ON u.id = r.owner_id
		LEFT JOIN room_members me ON me.room_id = r.id AND me.user_id = $7
		LEFT JOIN room_stars st ON st.room_id = r.id AND st.user_id = $7
		LEFT JOIN unnest($1::uuid[], $2::int[]) AS v(id, viewers) ON v.id = r.id
		LEFT JOIN queue_items qi ON qi.id = r.current_item_id
		LEFT JOIN media m ON m.id = qi.media_id
		WHERE (r.visibility = 'public' OR me.user_id IS NOT NULL)
		  AND ($7::uuid IS NULL OR NOT EXISTS (SELECT 1 FROM room_bans b WHERE b.room_id = r.id AND b.user_id = $7))
		  AND ($3 = '' OR r.name ILIKE '%' || $3 || '%' ESCAPE '\' OR r.slug ILIKE '%' || $3 || '%' ESCAPE '\')
		  AND (NOT $4 OR (r.playing AND m.status = 'ready'))
		  AND (NOT $8 OR r.visibility = 'private')
		  AND (NOT $9 OR me.user_id IS NOT NULL)
		  AND (NOT $10 OR st.user_id IS NOT NULL)
		ORDER BY ` + q.Sort.orderBy() + `
		OFFSET $5 LIMIT $6
	`
	rows, err := r.pool.Query(ctx, sql, q.LiveIDs, q.LiveCounts, escapeLike(q.Search), q.OnlyLive, q.Offset, q.Limit,
		q.ViewerID, q.OnlyPrivate, q.OnlyMine, q.OnlyStarred)
	if err != nil {
		return nil, 0, wrapErr(err)
	}
	defer rows.Close()

	var (
		out   []DirectoryRoom
		total int
	)
	for rows.Next() {
		var (
			dr                            DirectoryRoom
			settings                      []byte
			mediaID                       *uuid.UUID
			m                             entity.Media
			mStatus                       *entity.MediaStatus
			mProg                         *float32
			mRend                         []byte
			mCreated, mUpdated, mAccessed *time.Time
		)
		rm := &dr.Room
		if err := rows.Scan(&rm.ID, &rm.Slug, &rm.Name, &rm.OwnerID, &rm.Visibility, &settings, &rm.CurrentItemID,
			&rm.Playing, &rm.PositionMs, &rm.PositionAt, &rm.Rate, &rm.CreatedAt, &rm.UpdatedAt,
			&dr.Owner, &dr.MemberCount, &dr.Viewers, &dr.Live, &dr.MyRole, &dr.Starred,
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
			dr.Media = &media
		}
		out = append(out, dr)
	}
	return out, total, rows.Err()
}

// escapeLike makes user input literal inside an ILIKE pattern.
func escapeLike(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `%`, `\%`)
	return strings.ReplaceAll(s, `_`, `\_`)
}
