package mediastore

import (
	"testing"
	"time"

	"uuid"

	"github.com/stretchr/testify/assert"
)

func TestSigner(t *testing.T) {
	s := NewSigner("0123456789abcdef0123456789abcdef", time.Hour)
	other := NewSigner("ffffffffffffffffffffffffffffffff", time.Hour)
	id, id2 := uuid.New(), uuid.New()
	now := time.Unix(1_800_000_000, 0)

	tok := s.Sign(id, now)
	assert.True(t, s.Verify(tok, id, now))
	assert.True(t, s.Verify(tok, id, now.Add(59*time.Minute)))
	assert.False(t, s.Verify(tok, id, now.Add(61*time.Minute)), "expired")
	assert.False(t, s.Verify(tok, id2, now), "other media")
	assert.False(t, other.Verify(tok, id, now), "other secret")
	assert.False(t, s.Verify(tok+"x", id, now), "garbage")
	assert.False(t, s.Verify("", id, now))
}
