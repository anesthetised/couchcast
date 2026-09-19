package apihttp

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"uuid"

	"github.com/anesthetised/couchcast/internal/auth"
	"github.com/anesthetised/couchcast/internal/entity"
	"github.com/anesthetised/couchcast/internal/metrics"
	"github.com/anesthetised/couchcast/internal/ratelimit"
	"github.com/anesthetised/couchcast/internal/repository"
)

// fakeStore is an in-memory stand-in for the repository used by handler
// tests. It implements every store interface the router depends on.
type fakeStore struct {
	mu       sync.Mutex
	users    map[uuid.UUID]*entity.User
	sessions map[string]*entity.Session
	pingErr  error
}

func newFakeStore() *fakeStore {
	return &fakeStore{users: map[uuid.UUID]*entity.User{}, sessions: map[string]*entity.Session{}}
}

func (f *fakeStore) Ping(context.Context) error { return f.pingErr }

func (f *fakeStore) CreateUser(_ context.Context, username, hash string) (*entity.User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, u := range f.users {
		if u.Username == username {
			return nil, repository.ErrConflict
		}
	}
	u := &entity.User{ID: uuid.New(), Username: username, PasswordHash: hash, Role: entity.RoleUser, CreatedAt: time.Now()}
	f.users[u.ID] = u
	return u, nil
}

func (f *fakeStore) GetUserByUsername(_ context.Context, username string) (*entity.User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, u := range f.users {
		if u.Username == username {
			return u, nil
		}
	}
	return nil, repository.ErrNotFound
}

func (f *fakeStore) CreateSession(_ context.Context, h []byte, uid uuid.UUID, exp time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sessions[string(h)] = &entity.Session{TokenHash: h, UserID: uid, LastSeenAt: time.Now(), ExpiresAt: exp}
	return nil
}

func (f *fakeStore) GetSessionUser(_ context.Context, h []byte, now time.Time) (*entity.Session, *entity.User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	s, ok := f.sessions[string(h)]
	if !ok || !s.ExpiresAt.After(now) {
		return nil, nil, repository.ErrNotFound
	}
	return s, f.users[s.UserID], nil
}

func (f *fakeStore) TouchSession(_ context.Context, h []byte, now, exp time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if s, ok := f.sessions[string(h)]; ok {
		s.LastSeenAt, s.ExpiresAt = now, exp
	}
	return nil
}

func (f *fakeStore) DeleteSession(_ context.Context, h []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.sessions, string(h))
	return nil
}

func (f *fakeStore) DeleteUserSessions(_ context.Context, uid uuid.UUID) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var n int64
	for k, s := range f.sessions {
		if s.UserID == uid {
			delete(f.sessions, k)
			n++
		}
	}
	return n, nil
}

// testEnv bundles a router with its fake store and a cookie jar so tests
// read like a client session.
type testEnv struct {
	t       *testing.T
	store   *fakeStore
	handler http.Handler
	cookies []*http.Cookie
}

func newTestEnv(t *testing.T, static fstest.MapFS, opts ...func(*Deps)) *testEnv {
	t.Helper()
	store := newFakeStore()
	deps := Deps{
		Logger:       slog.New(slog.DiscardHandler),
		DB:           store,
		Metrics:      metrics.New("test"),
		Static:       static,
		Users:        store,
		Sessions:     auth.NewSessions(store, time.Hour, false),
		AuthLimiter:  ratelimit.New(600, 100),
		LoginLimiter: ratelimit.New(600, 100),
	}
	for _, opt := range opts {
		opt(&deps)
	}
	return &testEnv{t: t, store: store, handler: New(deps).Handler()}
}

// do performs a request, sending stored cookies and absorbing Set-Cookie.
func (e *testEnv) do(method, path string, body any) *httptest.ResponseRecorder {
	return e.doWith(method, path, body, nil)
}

// doWith is do with extra request headers.
func (e *testEnv) doWith(method, path string, body any, headers map[string]string) *httptest.ResponseRecorder {
	e.t.Helper()

	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			e.t.Fatal(err)
		}
	}

	req := httptest.NewRequest(method, "http://example.com"+path, &buf)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.RemoteAddr = "192.0.2.10:4000"
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	for _, c := range e.cookies {
		req.AddCookie(c)
	}

	rec := httptest.NewRecorder()
	e.handler.ServeHTTP(rec, req)

	for _, c := range rec.Result().Cookies() {
		e.setCookie(c)
	}

	return rec
}

func (e *testEnv) setCookie(c *http.Cookie) {
	for i, old := range e.cookies {
		if old.Name == c.Name {
			if c.MaxAge < 0 || c.Value == "" {
				e.cookies = append(e.cookies[:i], e.cookies[i+1:]...)
			} else {
				e.cookies[i] = c
			}
			return
		}
	}
	if c.MaxAge >= 0 && c.Value != "" {
		e.cookies = append(e.cookies, c)
	}
}

func decodeBody[T any](t *testing.T, rec *httptest.ResponseRecorder) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatalf("decode %q: %v", rec.Body.String(), err)
	}
	return v
}
