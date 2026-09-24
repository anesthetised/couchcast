package webpush

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/hkdf"
	"crypto/sha256"
	"testing"

	"github.com/stretchr/testify/require"
)

// decryptForTest is the user agent side of RFC 8291, to check what
// encrypt produces.
func decryptForTest(t *testing.T, body []byte, ua *ecdh.PrivateKey, auth []byte) []byte {
	t.Helper()
	require.Greater(t, len(body), 86)
	salt, idLen := body[:16], int(body[20])
	asPublic := body[21 : 21+idLen]
	asKey, err := ecdh.P256().NewPublicKey(asPublic)
	require.NoError(t, err)
	shared, err := ua.ECDH(asKey)
	require.NoError(t, err)
	prkKey, err := hkdf.Extract(sha256.New, shared, auth)
	require.NoError(t, err)
	ikm, err := hkdf.Expand(sha256.New, prkKey, "WebPush: info\x00"+string(ua.PublicKey().Bytes())+string(asPublic), 32)
	require.NoError(t, err)
	prk, err := hkdf.Extract(sha256.New, ikm, salt)
	require.NoError(t, err)
	cek, err := hkdf.Expand(sha256.New, prk, "Content-Encoding: aes128gcm\x00", 16)
	require.NoError(t, err)
	nonce, err := hkdf.Expand(sha256.New, prk, "Content-Encoding: nonce\x00", 12)
	require.NoError(t, err)
	block, err := aes.NewCipher(cek)
	require.NoError(t, err)
	gcm, err := cipher.NewGCM(block)
	require.NoError(t, err)
	plain, err := gcm.Open(nil, nonce, body[21+idLen:], nil)
	require.NoError(t, err)
	require.Equal(t, byte(0x02), plain[len(plain)-1], "last-record delimiter")
	return plain[:len(plain)-1]
}
