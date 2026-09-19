package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"uuid"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/anesthetised/couchcast/internal/entity"
	"github.com/anesthetised/couchcast/internal/repository"
)

// memStore is an in-memory SessionStore for unit tests.
type memStore struct {
	sessions map[string]*entity.Session
	users    map[uuid.UUID]*entity.User
	touched  int
}

func newMemStore() *memStore {
	return &memStore{sessions: map[string]*entity.Session{}, users: map[uuid.UUID]*entity.User{}}
}

func (m *memStore) CreateSession(_ context.Context, h []byte, uid uuid.UUID, exp time.Time) error {
	m.sessions[string(h)] = &entity.Session{TokenHash: h, UserID: uid, ExpiresAt: exp, LastSeenAt: exp.Add(-time.Hour)}
	return nil
}

func (m *memStore) GetSessionUser(_ context.Context, h []byte, now time.Time) (*entity.Session, *entity.User, error) {
	s, ok := m.sessions[string(h)]
	if !ok || !s.ExpiresAt.After(now) {
		return nil, nil, repository.ErrNotFound
	}
	return s, m.users[s.UserID], nil
}

func (m *memStore) TouchSession(_ context.Context, h []byte, now, exp time.Time) error {
	m.touched++
	if s, ok := m.sessions[string(h)]; ok {
		s.LastSeenAt, s.ExpiresAt = now, exp
	}
	return nil
}

func (m *memStore) DeleteSession(_ context.Context, h []byte) error {
	delete(m.sessions, string(h))
	return nil
}

func (m *memStore) DeleteUserSessions(_ context.Context, uid uuid.UUID) (int64, error) {
	var n int64
	for k, s := range m.sessions {
		if s.UserID == uid {
			delete(m.sessions, k)
			n++
		}
	}
	return n, nil
}

func cookieRequest(rec *httptest.ResponseRecorder) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	for _, c := range rec.Result().Cookies() {
		if c.Value != "" {
			r.AddCookie(c)
		}
	}
	return r
}

func TestSessionsLifecycle(t *testing.T) {
	store := newMemStore()
	user := &entity.User{ID: uuid.New(), Username: "alice"}
	store.users[user.ID] = user

	now := time.Unix(1_700_000_000, 0)
	s := NewSessions(store, time.Hour, true)
	s.now = func() time.Time { return now }
	ctx := context.Background()

	// Anonymous.
	u, err := s.Resolve(ctx, httptest.NewRequest(http.MethodGet, "/", nil))
	require.NoError(t, err)
	assert.Nil(t, u)

	// Issue sets a secure, http-only cookie.
	rec := httptest.NewRecorder()
	require.NoError(t, s.Issue(ctx, rec, httptest.NewRequest(http.MethodPost, "/login", nil), user.ID))
	cookies := rec.Result().Cookies()
	require.Len(t, cookies, 1)
	assert.Equal(t, CookieName, cookies[0].Name)
	assert.True(t, cookies[0].HttpOnly)
	assert.True(t, cookies[0].Secure)
	assert.Equal(t, http.SameSiteLaxMode, cookies[0].SameSite)

	req := cookieRequest(rec)
	u, err = s.Resolve(ctx, req)
	require.NoError(t, err)
	require.NotNil(t, u)
	assert.Equal(t, "alice", u.Username)
	assert.Equal(t, 0, store.touched, "fresh session is not touched")

	// After touchInterval the sliding expiry is extended once.
	now = now.Add(touchInterval + time.Minute)
	_, err = s.Resolve(ctx, req)
	require.NoError(t, err)
	assert.Equal(t, 1, store.touched)

	// Expired.
	now = now.Add(3 * time.Hour)
	u, err = s.Resolve(ctx, req)
	require.NoError(t, err)
	assert.Nil(t, u)

	// Re-issue rotates: old token is deleted.
	now = time.Unix(1_700_000_000, 0)
	rec2 := httptest.NewRecorder()
	require.NoError(t, s.Issue(ctx, rec2, req, user.ID))
	assert.Len(t, store.sessions, 1)

	// Banned users are rejected and logged out everywhere.
	banned := now
	user.BannedAt = &banned
	_, err = s.Resolve(ctx, cookieRequest(rec2))
	assert.ErrorIs(t, err, ErrBanned)
	assert.Empty(t, store.sessions)

	// Revoke clears the cookie.
	rec3 := httptest.NewRecorder()
	require.NoError(t, s.Revoke(ctx, rec3, cookieRequest(rec2)))
	c := rec3.Result().Cookies()
	require.Len(t, c, 1)
	assert.Equal(t, -1, c[0].MaxAge)
}

func TestCheckOrigin(t *testing.T) {
	ok := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	h := CheckOrigin(ok)

	cases := []struct {
		name    string
		method  string
		headers map[string]string
		want    int
	}{
		{"get without headers", http.MethodGet, nil, http.StatusNoContent},
		{"post without headers (non-browser)", http.MethodPost, nil, http.StatusNoContent},
		{"post same origin", http.MethodPost, map[string]string{"Origin": "http://example.com"}, http.StatusNoContent},
		{"post same origin case-insensitive", http.MethodPost, map[string]string{"Origin": "http://EXAMPLE.com"}, http.StatusNoContent},
		{"post cross origin", http.MethodPost, map[string]string{"Origin": "http://evil.com"}, http.StatusForbidden},
		{"post cross-site fetch metadata", http.MethodPost, map[string]string{"Sec-Fetch-Site": "cross-site"}, http.StatusForbidden},
		{"post same-origin fetch metadata", http.MethodPost, map[string]string{"Sec-Fetch-Site": "same-origin"}, http.StatusNoContent},
		{"post referer cross", http.MethodPost, map[string]string{"Referer": "http://evil.com/page"}, http.StatusForbidden},
		{"post referer same", http.MethodPost, map[string]string{"Referer": "http://example.com/page"}, http.StatusNoContent},
		{"get cross origin passes", http.MethodGet, map[string]string{"Origin": "http://evil.com"}, http.StatusNoContent},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(tc.method, "http://example.com/api/v1/x", nil)
			for k, v := range tc.headers {
				r.Header.Set(k, v)
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, r)
			assert.Equal(t, tc.want, rec.Code)
		})
	}
}

func TestRequireMiddlewares(t *testing.T) {
	ok := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })

	anon := httptest.NewRequest(http.MethodGet, "/", nil)
	user := anon.WithContext(WithUser(anon.Context(), &entity.User{Role: entity.RoleUser}))
	admin := anon.WithContext(WithUser(anon.Context(), &entity.User{Role: entity.RoleAdmin}))

	code := func(h http.Handler, r *http.Request) int {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, r)
		return rec.Code
	}

	assert.Equal(t, http.StatusUnauthorized, code(RequireUser(ok), anon))
	assert.Equal(t, http.StatusNoContent, code(RequireUser(ok), user))
	assert.Equal(t, http.StatusUnauthorized, code(RequireAdmin(ok), anon))
	assert.Equal(t, http.StatusForbidden, code(RequireAdmin(ok), user))
	assert.Equal(t, http.StatusNoContent, code(RequireAdmin(ok), admin))
}
