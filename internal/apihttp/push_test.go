package apihttp

import (
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/anesthetised/couchcast/internal/repository"
	"github.com/anesthetised/couchcast/internal/repository/repotest"
)

func TestPushSubscriptions(t *testing.T) {
	repo := repository.New(repotest.Pool(t))
	handler := newDBHandler(t, func(d *Deps) {
		d.Rooms, d.Users, d.DB, d.Push, d.PushPublicKey = repo, repo, repo, repo, "BPUBLIC"
	})
	env := &dbEnv{&testEnv{t: t, handler: handler}}
	assert.Equal(t, http.StatusUnauthorized, env.do(http.MethodGet, "/api/v1/push/key", nil).Code)
	env.register("ann")
	assert.Equal(t, "BPUBLIC", decodeBody[map[string]string](t, env.do(http.MethodGet, "/api/v1/push/key", nil))["publicKey"])

	key, err := ecdh.P256().GenerateKey(rand.Reader)
	require.NoError(t, err)
	enc := base64.RawURLEncoding.EncodeToString
	sub := func(endpoint, p256dh, auth string) map[string]any {
		return map[string]any{"endpoint": endpoint, "keys": map[string]string{"p256dh": p256dh, "auth": auth}}
	}
	good := sub("https://fcm.googleapis.com/fcm/send/abc", enc(key.PublicKey().Bytes()), enc(make([]byte, 16)))

	// Only known push services; well-formed keys.
	assert.Equal(t, http.StatusBadRequest, env.do(http.MethodPost, "/api/v1/push/subscriptions", sub("https://10.0.0.5/x", enc(key.PublicKey().Bytes()), enc(make([]byte, 16)))).Code)
	assert.Equal(t, http.StatusBadRequest, env.do(http.MethodPost, "/api/v1/push/subscriptions", sub("https://fcm.googleapis.com/fcm/send/abc", "short", enc(make([]byte, 16)))).Code)
	assert.Equal(t, http.StatusNoContent, env.do(http.MethodPost, "/api/v1/push/subscriptions", good).Code)

	ann, err := repo.GetUserByUsername(context.Background(), "ann")
	require.NoError(t, err)
	subs, err := repo.ListPushSubscriptions(context.Background(), ann.ID)
	require.NoError(t, err)
	require.Len(t, subs, 1)

	assert.Equal(t, http.StatusNoContent, env.do(http.MethodDelete, "/api/v1/push/subscriptions", good).Code)
	subs, _ = repo.ListPushSubscriptions(context.Background(), ann.ID)
	assert.Empty(t, subs)

	// Without a key the routes do not exist.
	plain := newDBHandler(t, func(d *Deps) { d.Push = repo })
	noPush := &dbEnv{&testEnv{t: t, handler: plain}}
	noPush.register("bob")
	assert.Equal(t, http.StatusNotFound, noPush.do(http.MethodGet, "/api/v1/push/key", nil).Code)
}
