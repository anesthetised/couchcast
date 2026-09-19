package ratelimit

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestAllowAndSweep(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	l := New(60, 2) // 1/s sustained, burst 2
	l.now = func() time.Time { return now }

	assert.True(t, l.Allow("a"))
	assert.True(t, l.Allow("a"))
	assert.False(t, l.Allow("a"))
	assert.True(t, l.Allow("b"), "keys are independent")

	now = now.Add(time.Second)
	assert.True(t, l.Allow("a"), "one token refilled")

	now = now.Add(idleTTL + time.Minute)
	l.Sweep()
	assert.Empty(t, l.buckets)
}

func TestMiddleware(t *testing.T) {
	l := New(60, 1)
	h := l.Middleware(ClientIP(false))(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req.RemoteAddr = "10.0.0.1:1234"

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusNoContent, rec.Code)

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusTooManyRequests, rec.Code)
	assert.Equal(t, "60", rec.Header().Get("Retry-After"))

	other := httptest.NewRequest(http.MethodPost, "/", nil)
	other.RemoteAddr = "10.0.0.2:1234"
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, other)
	assert.Equal(t, http.StatusNoContent, rec.Code)
}

func TestClientIP(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "192.0.2.1:5555"
	r.Header.Set("X-Forwarded-For", " 203.0.113.9, 10.0.0.1")

	assert.Equal(t, "192.0.2.1", ClientIP(false)(r))
	assert.Equal(t, "203.0.113.9", ClientIP(true)(r))
}
