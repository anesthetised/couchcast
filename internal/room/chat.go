package room

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"uuid"

	"golang.org/x/time/rate"

	"github.com/anesthetised/couchcast/internal/access"
	"github.com/anesthetised/couchcast/internal/entity"
	"github.com/anesthetised/couchcast/internal/notify"
	"github.com/anesthetised/couchcast/internal/protocol"
	"github.com/anesthetised/couchcast/internal/repository"
)

// ChatStore is the chat persistence used by rooms.
type ChatStore interface {
	CreateMessage(ctx context.Context, roomID, userID uuid.UUID, body string, replyTo *int64) (*entity.Message, error)
	CreateSystemMessage(ctx context.Context, roomID uuid.UUID, body string) (*entity.Message, error)
	ListRecentMessages(ctx context.Context, roomID uuid.UUID, limit int) ([]entity.Message, error)
	GetMessage(ctx context.Context, roomID uuid.UUID, id int64) (*entity.Message, error)
	DeleteMessage(ctx context.Context, roomID uuid.UUID, id int64, deletedBy uuid.UUID, onlyOwn bool) error
	ClearMessages(ctx context.Context, roomID uuid.UUID, deletedBy uuid.UUID) (int64, error)
	SetPinnedMessage(ctx context.Context, roomID uuid.UUID, id *int64) error
}

const (
	maxMessageLen  = 2000
	recentMessages = 100
)

func toChatMessage(m *entity.Message) protocol.ChatMessage {
	out := protocol.ChatMessage{Type: protocol.TypeChatMessage, ID: m.ID, Username: m.Username, Color: m.Color, Body: m.Body, System: m.System, CreatedMs: m.CreatedAt.UnixMilli()}
	if m.ReplyTo != nil {
		out.ReplyTo = &protocol.Quote{ID: m.ReplyTo.ID, Username: m.ReplyTo.Username, Body: m.ReplyTo.Body}
	}
	return out
}

// logLocked appends a system line to the room log and pushes it to every
// viewer. Failures are logged: the log must never break a command.
func (r *Room) logLocked(ctx context.Context, body string) {
	if r.deps.Chat == nil {
		return
	}
	msg, err := r.deps.Chat.CreateSystemMessage(ctx, r.info.ID, body)
	if err != nil {
		r.deps.Logger.Warn("room log", "room", r.info.Slug, "error", err)
		return
	}
	out := toChatMessage(msg)
	for _, v := range r.viewers {
		v.conn.Send(out)
	}
}

// mediaLabel names a media item for log lines: title when probed, else
// the source host.
func mediaLabel(m *entity.Media) string {
	if m == nil {
		return "a video"
	}
	if m.Title != "" {
		return "“" + m.Title + "”"
	}
	if u, err := url.Parse(m.SourceURL); err == nil && u.Host != "" {
		return "a video from " + strings.TrimPrefix(u.Host, "www.")
	}
	return "a video"
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
func (r *Room) ChatSend(ctx context.Context, actor access.Actor, body string, replyTo *int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.deps.Chat == nil {
		return invalid("chat is disabled")
	}
	if actor.Muted() {
		return &Error{Code: protocol.CodeForbidden, Message: "you are muted until " + actor.MutedUntil.Local().Format("15:04")}
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
	// Slow mode: non-moderators wait between messages.
	if slow := r.info.Settings.SlowModeSec; slow > 0 && !access.Can(actor, access.ModerateChat, r.info) {
		if wait := time.Duration(slow)*time.Second - r.now().Sub(r.lastChatAt[actor.User.ID]); wait > 0 {
			return &Error{Code: protocol.CodeRateLimit, Message: fmt.Sprintf("slow mode: wait %d s", int(wait.Seconds())+1)}
		}
		r.lastChatAt[actor.User.ID] = r.now()
	}

	msg, err := r.deps.Chat.CreateMessage(ctx, r.info.ID, actor.User.ID, body, replyTo)
	if errors.Is(err, repository.ErrNotFound) {
		return notFound("the message you are replying to")
	}
	if err != nil {
		return err
	}
	out := toChatMessage(msg)
	for _, v := range r.viewers {
		v.conn.Send(out)
	}
	r.notifyMentionsLocked(ctx, actor, msg)
	return nil
}

var mentionRe = regexp.MustCompile(`(?:^|[^\w@])@([A-Za-z0-9_]{3,32})`)

const (
	maxMentionPushes = 5
	// mentionPushEvery throttles pushes to one person from one room, so a
	// conversation does not buzz their phone on every line.
	mentionPushEvery = time.Minute
)

// notifyMentionsLocked pushes to mentioned users who are not in the room
// and may open it. Failures only log: chat must never fail on this.
func (r *Room) notifyMentionsLocked(ctx context.Context, actor access.Actor, msg *entity.Message) {
	if r.deps.Notifier == nil {
		return
	}
	var names []string
	seen := map[string]bool{strings.ToLower(actor.User.Username): true}
	for _, m := range mentionRe.FindAllStringSubmatch(msg.Body, -1) {
		n := strings.ToLower(m[1])
		if !seen[n] && len(names) < maxMentionPushes {
			seen[n] = true
			names = append(names, n)
		}
	}
	if len(names) == 0 {
		return
	}
	ids, err := r.deps.Store.UserIDsByUsername(ctx, names)
	if err != nil {
		r.deps.Logger.Warn("mention push: resolve users", "error", err)
		return
	}
	now := r.now()
	for _, id := range ids {
		if r.connectedLocked(id) || now.Sub(r.lastMention[id]) < mentionPushEvery {
			continue
		}
		if !r.info.IsPublic() {
			if _, err := r.deps.Store.GetMember(ctx, r.info.ID, id); err != nil {
				continue // cannot open the room: no push
			}
		}
		r.lastMention[id] = now
		body := msg.Body
		if utf8.RuneCountInString(body) > 140 {
			body = string([]rune(body)[:140]) + "…"
		}
		r.deps.Notifier.Notify(id, notify.Message{
			Title: actor.User.Username + " mentioned you in " + r.info.Name,
			Body:  body,
			URL:   "/r/" + r.info.Slug,
			Tag:   fmt.Sprintf("mention-%d", msg.ID),
		})
	}
}

// ChatTyping relays a typing hint to everyone else; nothing is stored.
func (r *Room) ChatTyping(actor access.Actor) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.requireLocked(actor, access.Chat); err != nil {
		return err
	}
	out := protocol.Typing{Type: protocol.TypeTyping, Username: actor.User.Username}
	for _, v := range r.viewers {
		if v.user != nil && v.user.ID == actor.User.ID {
			continue
		}
		v.conn.Send(out)
	}
	return nil
}

// Reactions is the fixed set a viewer may send.
var Reactions = map[string]bool{"👍": true, "❤️": true, "😂": true, "😮": true, "😢": true, "🔥": true, "👏": true, "🎉": true}

// React broadcasts an emoji to everyone (the sender included, so their
// own reaction floats too); one per second per user.
func (r *Room) React(actor access.Actor, emoji string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.requireLocked(actor, access.Chat); err != nil {
		return err
	}
	if !Reactions[emoji] {
		return invalid("unknown reaction")
	}
	if !r.reactLimiter(actor.User.ID).Allow() {
		return &Error{Code: protocol.CodeRateLimit, Message: "slow down"}
	}
	out := protocol.Reaction{Type: protocol.TypeReaction, Username: actor.User.Username, Emoji: emoji}
	for _, v := range r.viewers {
		v.conn.Send(out)
	}
	return nil
}

// reactLimiter is the per-user reaction budget: one a second, small burst.
func (r *Room) reactLimiter(userID uuid.UUID) *rate.Limiter {
	l, ok := r.reactLimits[userID]
	if !ok {
		l = rate.NewLimiter(rate.Every(time.Second), 3)
		r.reactLimits[userID] = l
	}
	return l
}

// ChatClear removes every message in the room (moderators only).
func (r *Room) ChatClear(ctx context.Context, actor access.Actor) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.deps.Chat == nil {
		return invalid("chat is disabled")
	}
	if err := r.requireLocked(actor, access.ModerateChat); err != nil {
		return err
	}
	if _, err := r.deps.Chat.ClearMessages(ctx, r.info.ID, actor.User.ID); err != nil {
		return err
	}
	out := protocol.ChatCleared{Type: protocol.TypeChatCleared}
	for _, v := range r.viewers {
		v.conn.Send(out)
	}
	if r.pinned != nil {
		r.setPinnedLocked(nil)
	}
	r.logLocked(ctx, actor.User.Username+" cleared the chat")
	return nil
}

// Log appends a system line from outside the room (REST moderation).
func (r *Room) Log(ctx context.Context, line string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.logLocked(ctx, line)
}

// ChatDelete removes a message (moderators only).
func (r *Room) ChatDelete(ctx context.Context, actor access.Actor, id int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.deps.Chat == nil {
		return invalid("chat is disabled")
	}
	// Moderators delete anything; everyone else only their own lines.
	onlyOwn := !access.Can(actor, access.ModerateChat, r.info)
	if onlyOwn {
		if err := r.requireLocked(actor, access.Chat); err != nil {
			return err
		}
	}
	err := r.deps.Chat.DeleteMessage(ctx, r.info.ID, id, actor.User.ID, onlyOwn)
	if errors.Is(err, repository.ErrNotFound) {
		if onlyOwn {
			return &Error{Code: protocol.CodeForbidden, Message: "you can only delete your own messages"}
		}
		return notFound("message")
	}
	if err != nil {
		return err
	}
	out := protocol.ChatDeleted{Type: protocol.TypeChatDeleted, ID: id}
	for _, v := range r.viewers {
		v.conn.Send(out)
	}
	if r.pinned != nil && r.pinned.ID == id {
		r.setPinnedLocked(nil)
	}
	return nil
}

// ChatPin pins a message above the chat; ChatUnpin clears it.
func (r *Room) ChatPin(ctx context.Context, actor access.Actor, id int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.deps.Chat == nil {
		return invalid("chat is disabled")
	}
	if err := r.requireLocked(actor, access.ModerateChat); err != nil {
		return err
	}
	msg, err := r.deps.Chat.GetMessage(ctx, r.info.ID, id)
	if errors.Is(err, repository.ErrNotFound) {
		return notFound("message")
	}
	if err != nil {
		return err
	}
	if msg.System {
		return invalid("system lines cannot be pinned")
	}
	if err := r.deps.Chat.SetPinnedMessage(ctx, r.info.ID, &id); err != nil {
		return err
	}
	pinned := toChatMessage(msg)
	pinned.Type = ""
	r.setPinnedLocked(&pinned)
	r.logLocked(ctx, actor.User.Username+" pinned a message")
	return nil
}

func (r *Room) ChatUnpin(ctx context.Context, actor access.Actor) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.deps.Chat == nil {
		return invalid("chat is disabled")
	}
	if err := r.requireLocked(actor, access.ModerateChat); err != nil {
		return err
	}
	if r.pinned == nil {
		return nil
	}
	if err := r.deps.Chat.SetPinnedMessage(ctx, r.info.ID, nil); err != nil {
		return err
	}
	r.setPinnedLocked(nil)
	return nil
}

// setPinnedLocked updates the pin and tells every viewer.
func (r *Room) setPinnedLocked(msg *protocol.ChatMessage) {
	r.pinned = msg
	out := protocol.ChatPinned{Type: protocol.TypeChatPinned, Message: msg}
	for _, v := range r.viewers {
		v.conn.Send(out)
	}
}

// loadPinnedLocked resolves the persisted pin; a deleted or missing
// message clears it.
func (r *Room) loadPinnedLocked(ctx context.Context) {
	r.pinned = nil
	if r.deps.Chat == nil || r.info.PinnedMessageID == nil {
		return
	}
	msg, err := r.deps.Chat.GetMessage(ctx, r.info.ID, *r.info.PinnedMessageID)
	if err != nil {
		if !errors.Is(err, repository.ErrNotFound) {
			r.deps.Logger.Warn("load pinned message", "room", r.info.Slug, "error", err)
		}
		return
	}
	pinned := toChatMessage(msg)
	pinned.Type = ""
	r.pinned = &pinned
}
