package webpush

import (
	"context"
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/sha256"
	"encoding/json"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func dec(t *testing.T, s string) []byte {
	t.Helper()
	b, err := b64.DecodeString(s)
	require.NoError(t, err)
	return b
}

// TestEncryptRFC8291 replays the worked example of RFC 8291 section 5.
func TestEncryptRFC8291(t *testing.T) {
	plaintext := dec(t, "V2hlbiBJIGdyb3cgdXAsIEkgd2FudCB0byBiZSBhIHdhdGVybWVsb24")
	asPriv := dec(t, "yfWPiYE-n46HLnH0KqZOF1fJJU3MYrct3AELtAQ-oRw")
	asPub := dec(t, "BP4z9KsN6nGRTbVYI_c7VJSPQTBtkgcy27mlmlMoZIIgDll6e3vCYLocInmYWAmS6TlzAC8wEqKK6PBru3jl7A8")
	uaPub := dec(t, "BCVxsr7N_eNgVRqvHtD0zTZsEc6-VV-JvLexhqUzORcxaOzi6-AYWXvTBHm4bjyPjs7Vd8pZGH6SRpkNtoIAiw4")
	salt := dec(t, "DGv6ra1nlYgDCS1FRnbzlw")
	auth := dec(t, "BTBZMqHH6r4Tts7J_aSIgg")

	key, err := ecdh.P256().NewPrivateKey(asPriv)
	require.NoError(t, err)
	require.Equal(t, asPub, key.PublicKey().Bytes())

	body, err := encrypt(plaintext, uaPub, auth, salt, key)
	require.NoError(t, err)

	// Header: salt, record size 4096, key id length 65, the server key.
	assert.Equal(t, salt, body[:16])
	assert.Equal(t, []byte{0, 0, 0x10, 0}, body[16:20])
	assert.Equal(t, byte(65), body[20])
	assert.Equal(t, asPub, body[21:86])
	assert.Equal(t, "8pfeW0KbunFT06SuDKoJH9Ql87S1QUrdirN6GcG7sFz1y1sqLgVi1VhjVkHsUoEsbI_0LpXMuGvnzQ", b64.EncodeToString(body[86:]))

	// The user agent can open it with its private key.
	uaKey, err := ecdh.P256().NewPrivateKey(dec(t, "q1dXpw3UpT5VOmu_cf_v6ih07Aems3njxI-JWgLcM94"))
	require.NoError(t, err)
	assert.Equal(t, plaintext, decryptForTest(t, body, uaKey, auth))
}

func TestEncryptRoundTrip(t *testing.T) {
	ua, err := ecdh.P256().GenerateKey(nil)
	require.NoError(t, err)
	auth := []byte("0123456789abcdef")
	sub := Subscription{Endpoint: "https://push.example/x", P256dh: b64.EncodeToString(ua.PublicKey().Bytes()), Auth: b64.EncodeToString(auth)}
	body, err := Encrypt([]byte(`{"title":"hi"}`), sub)
	require.NoError(t, err)
	assert.Equal(t, `{"title":"hi"}`, string(decryptForTest(t, body, ua, auth)))

	_, err = Encrypt(make([]byte, 5000), sub)
	assert.Error(t, err)
}

func TestVAPID(t *testing.T) {
	pub, priv, err := GenerateVAPID()
	require.NoError(t, err)
	v, err := ParseVAPID(pub, priv, "mailto:admin@example.com")
	require.NoError(t, err)
	assert.Equal(t, pub, v.PublicKey())
	_, err = ParseVAPID("Bwrong", priv, "mailto:x@y")
	assert.Error(t, err)
	_, err = ParseVAPID(pub, priv, "")
	assert.Error(t, err)

	now := time.Unix(1_800_000_000, 0)
	h, err := v.authorization("https://fcm.googleapis.com/fcm/send/abc", now)
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(h, "vapid t="))
	tok, k, _ := strings.Cut(strings.TrimPrefix(h, "vapid t="), ", k=")
	assert.Equal(t, pub, k)
	parts := strings.Split(tok, ".")
	require.Len(t, parts, 3)
	var claims map[string]any
	require.NoError(t, json.Unmarshal(dec(t, parts[1]), &claims))
	assert.Equal(t, "https://fcm.googleapis.com", claims["aud"])
	assert.Equal(t, "mailto:admin@example.com", claims["sub"])
	assert.EqualValues(t, now.Add(12*time.Hour).Unix(), claims["exp"])

	// The signature verifies with the public key.
	pk, err := ecdsa.ParseUncompressedPublicKey(elliptic.P256(), dec(t, pub))
	require.NoError(t, err)
	sig := dec(t, parts[2])
	require.Len(t, sig, 64)
	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	assert.True(t, ecdsa.Verify(pk, digest[:], new(big.Int).SetBytes(sig[:32]), new(big.Int).SetBytes(sig[32:])))
}

func TestSend(t *testing.T) {
	pub, priv, err := GenerateVAPID()
	require.NoError(t, err)
	v, err := ParseVAPID(pub, priv, "mailto:a@b")
	require.NoError(t, err)
	ua, err := ecdh.P256().GenerateKey(nil)
	require.NoError(t, err)
	auth := []byte("0123456789abcdef")

	var got *http.Request
	var body []byte
	status := http.StatusCreated
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r
		body, _ = io.ReadAll(r.Body)
		w.WriteHeader(status)
	}))
	defer srv.Close()

	c := &Client{VAPID: v, HTTP: srv.Client()}
	sub := Subscription{Endpoint: srv.URL + "/push/1", P256dh: b64.EncodeToString(ua.PublicKey().Bytes()), Auth: b64.EncodeToString(auth)}
	require.NoError(t, c.Send(context.Background(), sub, []byte("hello"), Options{TTL: time.Hour, Urgency: "high", Topic: "invite"}))
	assert.Equal(t, "aes128gcm", got.Header.Get("Content-Encoding"))
	assert.Equal(t, "3600", got.Header.Get("TTL"))
	assert.Equal(t, "high", got.Header.Get("Urgency"))
	assert.Equal(t, "invite", got.Header.Get("Topic"))
	assert.True(t, strings.HasPrefix(got.Header.Get("Authorization"), "vapid t="))
	assert.Equal(t, "hello", string(decryptForTest(t, body, ua, auth)))

	status = http.StatusGone
	assert.ErrorIs(t, c.Send(context.Background(), sub, []byte("x"), Options{}), ErrGone)
	status = http.StatusBadRequest
	err = c.Send(context.Background(), sub, []byte("x"), Options{})
	assert.Error(t, err)
	assert.NotErrorIs(t, err, ErrGone)
}

func TestAllowedEndpoint(t *testing.T) {
	for _, ok := range []string{
		"https://fcm.googleapis.com/fcm/send/abc",
		"https://updates.push.services.mozilla.com/wpush/v2/abc",
		"https://web.push.apple.com/QG-abc",
		"https://wns2-par02p.notify.windows.com/w/?token=abc",
	} {
		assert.True(t, AllowedEndpoint(ok), ok)
	}
	for _, bad := range []string{
		"http://fcm.googleapis.com/fcm/send/abc",
		"https://fcm.googleapis.com:8443/x",
		"https://evil.example/fcm.googleapis.com",
		"https://fcm.googleapis.com.evil.example/x",
		"https://user@fcm.googleapis.com/x",
		"https://localhost/x",
		"https://169.254.169.254/latest",
		"not a url",
	} {
		assert.False(t, AllowedEndpoint(bad), bad)
	}
}
