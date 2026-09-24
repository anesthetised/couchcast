package notify

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"uuid"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/anesthetised/couchcast/internal/repository"
	"github.com/anesthetised/couchcast/internal/webpush"
)

type fakeStore struct {
	mu      sync.Mutex
	subs    map[uuid.UUID][]repository.PushSubscription
	deleted []string
	touched []uuid.UUID
}

func (f *fakeStore) ListPushSubscriptions(_ context.Context, user uuid.UUID) ([]repository.PushSubscription, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.subs[user], nil
}

func (f *fakeStore) DeletePushEndpoint(_ context.Context, endpoint string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deleted = append(f.deleted, endpoint)
	return nil
}

func (f *fakeStore) TouchPushSubscription(_ context.Context, id uuid.UUID, _ time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.touched = append(f.touched, id)
	return nil
}

type sent struct {
	endpoint string
	msg      Message
	topic    string
}

type fakeSender struct {
	mu   sync.Mutex
	gone map[string]bool
	out  []sent
	done chan struct{}
}

func (f *fakeSender) Send(_ context.Context, sub webpush.Subscription, payload []byte, opts webpush.Options) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	defer func() { f.done <- struct{}{} }()
	if f.gone[sub.Endpoint] {
		return webpush.ErrGone
	}
	var m Message
	_ = json.Unmarshal(payload, &m)
	f.out = append(f.out, sent{endpoint: sub.Endpoint, msg: m, topic: opts.Topic})
	return nil
}

func TestNotifier(t *testing.T) {
	user, other := uuid.New(), uuid.New()
	live, stale := uuid.New(), uuid.New()
	store := &fakeStore{subs: map[uuid.UUID][]repository.PushSubscription{
		user: {{ID: live, Endpoint: "https://push/live"}, {ID: stale, Endpoint: "https://push/stale"}},
	}}
	sender := &fakeSender{gone: map[string]bool{"https://push/stale": true}, done: make(chan struct{}, 8)}
	n := New(store, sender, slog.New(slog.NewTextHandler(io.Discard, nil)))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = n.Run(ctx) }()

	n.Notify(other, Message{Title: "nobody listens"}) // no subscriptions: nothing sent
	n.Notify(user, Message{Title: "Room invite", Body: "hi", URL: "/", Tag: "invite-1/ä"})
	for range 2 {
		select {
		case <-sender.done:
		case <-time.After(2 * time.Second):
			t.Fatal("delivery did not happen")
		}
	}
	require.Eventually(t, func() bool {
		store.mu.Lock()
		defer store.mu.Unlock()
		return len(store.touched) == 1 && len(store.deleted) == 1
	}, 2*time.Second, 10*time.Millisecond)

	sender.mu.Lock()
	defer sender.mu.Unlock()
	require.Len(t, sender.out, 1)
	assert.Equal(t, "https://push/live", sender.out[0].endpoint)
	assert.Equal(t, "Room invite", sender.out[0].msg.Title)
	assert.Equal(t, "invite-1", sender.out[0].topic, "topic keeps only URL-safe characters")
	assert.Equal(t, []string{"https://push/stale"}, store.deleted, "gone subscriptions are removed")
	assert.Equal(t, []uuid.UUID{live}, store.touched)

	var nilNotifier *Notifier
	nilNotifier.Notify(user, Message{}) // must not panic
}
