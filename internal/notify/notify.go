// Package notify delivers Web Push notifications to a user's browsers.
// Callers hand over a message and move on: deliveries run in the
// background, a full queue drops messages rather than slowing the caller,
// and subscriptions the push service no longer knows are deleted.
package notify

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"sync"
	"time"

	"uuid"

	"github.com/anesthetised/couchcast/internal/repository"
	"github.com/anesthetised/couchcast/internal/webpush"
)

// Message is what the service worker shows. Tag replaces an earlier
// notification with the same tag (the page uses the same tags for its own
// in-tab notifications, so a user never sees one twice).
type Message struct {
	Title string `json:"title"`
	Body  string `json:"body"`
	URL   string `json:"url"`
	Tag   string `json:"tag"`
}

// Store is the subscription persistence.
type Store interface {
	ListPushSubscriptions(ctx context.Context, userID uuid.UUID) ([]repository.PushSubscription, error)
	DeletePushEndpoint(ctx context.Context, endpoint string) error
	TouchPushSubscription(ctx context.Context, id uuid.UUID, at time.Time) error
}

// Sender posts one encrypted message (webpush.Client).
type Sender interface {
	Send(ctx context.Context, sub webpush.Subscription, payload []byte, opts webpush.Options) error
}

type job struct {
	user uuid.UUID
	msg  Message
}

// Notifier queues and delivers messages. A nil Notifier drops everything,
// so callers need no "is push configured" checks.
type Notifier struct {
	store  Store
	sender Sender
	logger *slog.Logger
	jobs   chan job
	now    func() time.Time
	wg     sync.WaitGroup
}

const (
	queueSize = 256
	workers   = 2
	// sendTimeout bounds one delivery; push services answer quickly.
	sendTimeout = 10 * time.Second
	// ttl is how long a push service keeps a message for an offline
	// browser: a reminder that arrives a day late is noise.
	ttl = 4 * time.Hour
)

// New creates a notifier; Run starts delivering.
func New(store Store, sender Sender, logger *slog.Logger) *Notifier {
	return &Notifier{store: store, sender: sender, logger: logger, jobs: make(chan job, queueSize), now: time.Now}
}

// Notify queues a message for every browser of the user.
func (n *Notifier) Notify(user uuid.UUID, msg Message) {
	if n == nil {
		return
	}
	select {
	case n.jobs <- job{user: user, msg: msg}:
	default:
		n.logger.Warn("push queue full, message dropped", "tag", msg.Tag)
	}
}

// Run delivers until the context ends.
func (n *Notifier) Run(ctx context.Context) error {
	for range workers {
		n.wg.Go(func() {
			for {
				select {
				case <-ctx.Done():
					return
				case j := <-n.jobs:
					n.deliver(ctx, j)
				}
			}
		})
	}
	n.wg.Wait()
	return ctx.Err()
}

func (n *Notifier) deliver(ctx context.Context, j job) {
	subs, err := n.store.ListPushSubscriptions(ctx, j.user)
	if err != nil {
		n.logger.Warn("push: list subscriptions", "error", err)
		return
	}
	if len(subs) == 0 {
		return
	}
	payload, err := json.Marshal(j.msg)
	if err != nil {
		return
	}
	for _, s := range subs {
		sctx, cancel := context.WithTimeout(ctx, sendTimeout)
		err := n.sender.Send(sctx, webpush.Subscription{Endpoint: s.Endpoint, P256dh: s.P256dh, Auth: s.Auth}, payload, webpush.Options{TTL: ttl, Topic: topic(j.msg.Tag)})
		cancel()
		switch {
		case errors.Is(err, webpush.ErrGone):
			if err := n.store.DeletePushEndpoint(ctx, s.Endpoint); err != nil {
				n.logger.Warn("push: delete gone subscription", "error", err)
			}
		case err != nil:
			n.logger.Warn("push: deliver", "tag", j.msg.Tag, "error", err)
		default:
			_ = n.store.TouchPushSubscription(ctx, s.ID, n.now())
		}
	}
}

// topic turns a tag into a Topic header value (URL-safe base64 alphabet,
// at most 32 characters), so a newer message replaces an undelivered one.
func topic(tag string) string {
	out := make([]byte, 0, 32)
	for i := 0; i < len(tag) && len(out) < 32; i++ {
		c := tag[i]
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_' {
			out = append(out, c)
		}
	}
	return string(out)
}
