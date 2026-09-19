package repository

import (
	"context"
	"encoding/json"

	"github.com/anesthetised/couchcast/internal/entity"
)

// RecordAudit appends an audit entry. Failures are returned so callers can
// log them, but they should not fail the user-facing action.
func (r *Repo) RecordAudit(ctx context.Context, e entity.AuditEntry) error {
	meta := e.Meta
	if meta == nil {
		meta = map[string]any{}
	}
	b, err := json.Marshal(meta)
	if err != nil {
		return err
	}

	const q = `
		INSERT INTO audit_log (actor_id, action, target_type, target_id, room_id, meta)
		VALUES ($1, $2, $3, $4, $5, $6)
	`
	_, err = r.pool.Exec(ctx, q, e.ActorID, e.Action, e.TargetType, e.TargetID, e.RoomID, b)
	return wrapErr(err)
}
