// Package mediastore stores packaged media in S3-compatible storage and
// serves it to viewers behind short-lived HMAC tokens.
package mediastore

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"time"

	"uuid"
)

// Signer issues and verifies media tokens. A token binds one media id to an
// expiry; it is handed to viewers who have access to a room containing the
// media and appended to every segment request.
type Signer struct {
	secret []byte
	ttl    time.Duration
}

// NewSigner creates a signer. secret must be at least 32 bytes.
func NewSigner(secret string, ttl time.Duration) *Signer {
	return &Signer{secret: []byte(secret), ttl: ttl}
}

const macLen = 16

// Sign returns a token for the media valid for the signer's TTL.
func (s *Signer) Sign(mediaID uuid.UUID, now time.Time) string {
	exp := now.Add(s.ttl).Unix()

	buf := make([]byte, 0, 16+8+macLen)
	buf = append(buf, mediaID[:]...)
	buf = binary.BigEndian.AppendUint64(buf, uint64(exp)) //nolint:gosec // unix seconds are positive
	buf = append(buf, s.mac(buf)...)

	return base64.RawURLEncoding.EncodeToString(buf)
}

// Verify reports whether token grants access to mediaID at time now.
func (s *Signer) Verify(token string, mediaID uuid.UUID, now time.Time) bool {
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || len(raw) != 16+8+macLen {
		return false
	}

	body, mac := raw[:24], raw[24:]
	if !hmac.Equal(mac, s.mac(body)) {
		return false
	}
	if [16]byte(body[:16]) != [16]byte(mediaID) {
		return false
	}
	exp := int64(binary.BigEndian.Uint64(body[16:24])) //nolint:gosec // written by Sign
	return now.Unix() < exp
}

func (s *Signer) mac(body []byte) []byte {
	h := hmac.New(sha256.New, s.secret)
	h.Write(body)
	return h.Sum(nil)[:macLen]
}
