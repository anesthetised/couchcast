package room

import (
	"context"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"uuid"

	"golang.org/x/time/rate"

	"github.com/anesthetised/couchcast/internal/access"
	"github.com/anesthetised/couchcast/internal/entity"
	"github.com/anesthetised/couchcast/internal/protocol"
	"github.com/anesthetised/couchcast/internal/repository"
)

// ChatStore is the chat persistence used by rooms.
type ChatStore interface {
	CreateMessage(ctx context.Context, roomID, userID uuid.UUID, body string) (*entity.Message, error)
	ListRecentMessages(ctx context.Context, roomID uuid.UUID, limit int) ([]entity.Message, error)
	DeleteMessage(ctx context.Context, roomID uuid.UUID, id int64, deletedBy uuid.UUID) error
}

const (
	maxMessageLen  = 2000
	recentMessages = 100
)

func toChatMessage(m *entity.Message) protocol.ChatMessage {
	return protocol.ChatMessage{Type: protocol.TypeChatMessage, ID: m.ID, Username: m.Username, Body: m.Body, CreatedMs: m.CreatedAt.UnixMilli()}
}

// recentMessagesLocked loads the welcome backlog.
func (r *Room) recentMessages(ctx context.Context) []protocol.ChatMessage {
	if r.deps.Chat == nil {
		return nil
	}
	msgs, err := r.deps.Chat.ListRecentMessages(ctx, r.info.ID, recentMessages)
	if err != nil {
		r.deps.Logger.Warn("load chat backlog", "room", r.info.Slug, "error", err)
		return nil
	}
	out := make([]protocol.ChatMessage, 0, len(msgs))
	for i := range msgs {
		out = append(out, toChatMessage(&msgs[i]))
	}
	return out
}

// chatLimiter returns the per-user flood limiter, creating it on demand.
func (r *Room) chatLimiter(userID uuid.UUID) *rate.Limiter {
	l, ok := r.chatLimits[userID]
	if !ok {
		l = rate.NewLimiter(rate.Every(time.Second), 5)
		r.chatLimits[userID] = l
	}
	return l
}

// ChatSend posts a message from the actor to everyone in the room.
func (r *Room) ChatSend(ctx context.Context, actor access.Actor, body string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.deps.Chat == nil {
		return invalid("chat is disabled")
	}
	if err := r.requireLocked(actor, access.Chat); err != nil {
		return err
	}
	body = strings.TrimSpace(body)
	if body == "" || utf8.RuneCountInString(body) > maxMessageLen {
		return invalid("message must be between 1 and 2000 characters")
	}
	if !r.chatLimiter(actor.User.ID).Allow() {
		return &Error{Code: protocol.CodeRateLimit, Message: "you are sending messages too quickly"}
	}

	msg, err := r.deps.Chat.CreateMessage(ctx, r.info.ID, actor.User.ID, body)
	if err != nil {
		return err
	}
	out := toChatMessage(msg)
	for _, v := range r.viewers {
		v.conn.Send(out)
	}
	return nil
}

// ChatDelete removes a message (moderators only).
func (r *Room) ChatDelete(ctx context.Context, actor access.Actor, id int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.deps.Chat == nil {
		return invalid("chat is disabled")
	}
	if err := r.requireLocked(actor, access.ModerateChat); err != nil {
		return err
	}
	err := r.deps.Chat.DeleteMessage(ctx, r.info.ID, id, actor.User.ID)
	if errors.Is(err, repository.ErrNotFound) {
		return notFound("message")
	}
	if err != nil {
		return err
	}
	out := protocol.ChatDeleted{Type: protocol.TypeChatDeleted, ID: id}
	for _, v := range r.viewers {
		v.conn.Send(out)
	}
	return nil
}
