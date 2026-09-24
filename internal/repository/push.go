package repository

import (
	"context"
	"time"

	"uuid"
)

// PushSubscription is one browser's Web Push subscription.
type PushSubscription struct {
	ID       uuid.UUID
	UserID   uuid.UUID
	Endpoint string
	P256dh   string
	Auth     string
}

// SavePushSubscription stores a subscription for the user; an endpoint
// seen before (the same browser, perhaps another account) moves over.
func (r *Repo) SavePushSubscription(ctx context.Context, userID uuid.UUID, endpoint, p256dh, auth, userAgent string) error {
	const q = `
		INSERT INTO push_subscriptions (user_id, endpoint, p256dh, auth, user_agent) VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (endpoint) DO UPDATE SET user_id = EXCLUDED.user_id, p256dh = EXCLUDED.p256dh, auth = EXCLUDED.auth,
			user_agent = EXCLUDED.user_agent, created_at = now()
	`
	_, err := r.pool.Exec(ctx, q, userID, endpoint, p256dh, auth, userAgent)
	return wrapErr(err)
}

// DeletePushSubscription removes the user's subscription for an endpoint.
func (r *Repo) DeletePushSubscription(ctx context.Context, userID uuid.UUID, endpoint string) error {
	_, err := r.pool.Exec(ctx, `DELETE FROM push_subscriptions WHERE user_id = $1 AND endpoint = $2`, userID, endpoint)
	return wrapErr(err)
}

// DeletePushEndpoint drops a subscription the push service reported gone.
func (r *Repo) DeletePushEndpoint(ctx context.Context, endpoint string) error {
	_, err := r.pool.Exec(ctx, `DELETE FROM push_subscriptions WHERE endpoint = $1`, endpoint)
	return wrapErr(err)
}

// ListPushSubscriptions returns every subscription of the user.
func (r *Repo) ListPushSubscriptions(ctx context.Context, userID uuid.UUID) ([]PushSubscription, error) {
	rows, err := r.pool.Query(ctx, `SELECT id, user_id, endpoint, p256dh, auth FROM push_subscriptions WHERE user_id = $1`, userID)
	if err != nil {
		return nil, wrapErr(err)
	}
	defer rows.Close()
	var out []PushSubscription
	for rows.Next() {
		var s PushSubscription
		if err := rows.Scan(&s.ID, &s.UserID, &s.Endpoint, &s.P256dh, &s.Auth); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// TouchPushSubscription records a successful delivery.
func (r *Repo) TouchPushSubscription(ctx context.Context, id uuid.UUID, at time.Time) error {
	_, err := r.pool.Exec(ctx, `UPDATE push_subscriptions SET last_used_at = $2 WHERE id = $1`, id, at)
	return wrapErr(err)
}

// ScheduleReminder is a room whose announced start is near, with the
// members to remind.
type ScheduleReminder struct {
	RoomID      uuid.UUID
	Slug        string
	Name        string
	ScheduledAt time.Time
	Members     []uuid.UUID
}

// ClaimScheduleReminders marks rooms starting within lead as reminded
// and returns them; each announced start is claimed once, so concurrent
// servers never remind twice.
func (r *Repo) ClaimScheduleReminders(ctx context.Context, now time.Time, lead time.Duration) ([]ScheduleReminder, error) {
	const q = `
		UPDATE rooms SET reminded_for = scheduled_at
		WHERE scheduled_at > $1 AND scheduled_at <= $2 AND reminded_for IS DISTINCT FROM scheduled_at
		RETURNING id, slug, name, scheduled_at, ARRAY(SELECT user_id FROM room_members m WHERE m.room_id = rooms.id)
	`
	rows, err := r.pool.Query(ctx, q, now, now.Add(lead))
	if err != nil {
		return nil, wrapErr(err)
	}
	defer rows.Close()
	var out []ScheduleReminder
	for rows.Next() {
		var s ScheduleReminder
		if err := rows.Scan(&s.RoomID, &s.Slug, &s.Name, &s.ScheduledAt, &s.Members); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// UserIDsByUsername resolves usernames (case-insensitively) to ids;
// unknown names are absent from the map, which is keyed by the lower-case
// name.
func (r *Repo) UserIDsByUsername(ctx context.Context, names []string) (map[string]uuid.UUID, error) {
	rows, err := r.pool.Query(ctx, `SELECT lower(username), id FROM users WHERE lower(username) = ANY($1)`, names)
	if err != nil {
		return nil, wrapErr(err)
	}
	defer rows.Close()
	out := make(map[string]uuid.UUID, len(names))
	for rows.Next() {
		var (
			name string
			id   uuid.UUID
		)
		if err := rows.Scan(&name, &id); err != nil {
			return nil, err
		}
		out[name] = id
	}
	return out, rows.Err()
}
