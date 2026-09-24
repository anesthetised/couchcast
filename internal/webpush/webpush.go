// Package webpush sends Web Push messages with the standard library only:
// payloads are encrypted per RFC 8291 (aes128gcm, RFC 8188) and requests
// are authorised with VAPID (RFC 8292).
package webpush

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

var b64 = base64.RawURLEncoding

// Subscription is what the browser's PushManager hands out.
type Subscription struct {
	Endpoint string
	P256dh   string // base64url, uncompressed P-256 point
	Auth     string // base64url, 16 bytes
}

// VAPID holds the application server's key pair.
type VAPID struct {
	key     *ecdsa.PrivateKey
	public  []byte // uncompressed point
	subject string // mailto: or https: contact
}

// GenerateVAPID creates a key pair, base64url encoded (public key as an
// uncompressed point, private key as the raw scalar).
func GenerateVAPID() (public, private string, err error) {
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return "", "", err
	}
	pub, err := k.PublicKey.Bytes()
	if err != nil {
		return "", "", err
	}
	priv, err := k.Bytes()
	if err != nil {
		return "", "", err
	}
	return b64.EncodeToString(pub), b64.EncodeToString(priv), nil
}

// ParseVAPID reads a key pair produced by GenerateVAPID.
func ParseVAPID(public, private, subject string) (*VAPID, error) {
	raw, err := b64.DecodeString(private)
	if err != nil {
		return nil, fmt.Errorf("webpush: private key: %w", err)
	}
	k, err := ecdsa.ParseRawPrivateKey(elliptic.P256(), raw)
	if err != nil {
		return nil, fmt.Errorf("webpush: private key: %w", err)
	}
	pub, err := k.PublicKey.Bytes()
	if err != nil {
		return nil, err
	}
	if public != "" && public != b64.EncodeToString(pub) {
		return nil, errors.New("webpush: public key does not match the private key")
	}
	if subject == "" {
		return nil, errors.New("webpush: a VAPID subject (mailto: or https: URL) is required")
	}
	return &VAPID{key: k, public: pub, subject: subject}, nil
}

// PublicKey is the applicationServerKey the browser subscribes with.
func (v *VAPID) PublicKey() string { return b64.EncodeToString(v.public) }

// authorization builds the VAPID Authorization header for an endpoint:
// an ES256 JWT for the endpoint's origin plus the public key.
func (v *VAPID) authorization(endpoint string, now time.Time) (string, error) {
	u, err := url.Parse(endpoint)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return "", fmt.Errorf("webpush: endpoint %q is not an absolute URL", endpoint)
	}
	header := b64.EncodeToString([]byte(`{"typ":"JWT","alg":"ES256"}`))
	claims, err := json.Marshal(map[string]any{
		"aud": u.Scheme + "://" + u.Host,
		"exp": now.Add(12 * time.Hour).Unix(),
		"sub": v.subject,
	})
	if err != nil {
		return "", err
	}
	signing := header + "." + b64.EncodeToString(claims)
	digest := sha256.Sum256([]byte(signing))
	r, s, err := ecdsa.Sign(rand.Reader, v.key, digest[:])
	if err != nil {
		return "", err
	}
	sig := make([]byte, 64)
	r.FillBytes(sig[:32])
	s.FillBytes(sig[32:])
	return "vapid t=" + signing + "." + b64.EncodeToString(sig) + ", k=" + v.PublicKey(), nil
}

// recordSize is the aes128gcm record size advertised in the header; one
// record holds any payload a push service accepts (4 KB).
const recordSize = 4096

// Encrypt seals a payload for one subscription with a fresh ephemeral key
// and salt.
func Encrypt(payload []byte, sub Subscription) ([]byte, error) {
	eph, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	salt := make([]byte, 16)
	if _, err := io.ReadFull(rand.Reader, salt); err != nil {
		return nil, err
	}
	uaPublic, err := b64.DecodeString(sub.P256dh)
	if err != nil {
		return nil, fmt.Errorf("webpush: p256dh: %w", err)
	}
	auth, err := b64.DecodeString(sub.Auth)
	if err != nil {
		return nil, fmt.Errorf("webpush: auth: %w", err)
	}
	return encrypt(payload, uaPublic, auth, salt, eph)
}

// encrypt is RFC 8291 section 3 with the random inputs passed in, so the
// RFC's worked example can be replayed in tests.
func encrypt(payload, uaPublic, auth, salt []byte, asKey *ecdh.PrivateKey) ([]byte, error) {
	if len(payload) > recordSize-17-86 {
		return nil, errors.New("webpush: payload too large")
	}
	uaKey, err := ecdh.P256().NewPublicKey(uaPublic)
	if err != nil {
		return nil, fmt.Errorf("webpush: p256dh: %w", err)
	}
	if len(auth) != 16 {
		return nil, errors.New("webpush: auth secret must be 16 bytes")
	}
	shared, err := asKey.ECDH(uaKey)
	if err != nil {
		return nil, err
	}
	asPublic := asKey.PublicKey().Bytes()

	prkKey, err := hkdf.Extract(sha256.New, shared, auth)
	if err != nil {
		return nil, err
	}
	keyInfo := "WebPush: info\x00" + string(uaPublic) + string(asPublic)
	ikm, err := hkdf.Expand(sha256.New, prkKey, keyInfo, 32)
	if err != nil {
		return nil, err
	}
	prk, err := hkdf.Extract(sha256.New, ikm, salt)
	if err != nil {
		return nil, err
	}
	cek, err := hkdf.Expand(sha256.New, prk, "Content-Encoding: aes128gcm\x00", 16)
	if err != nil {
		return nil, err
	}
	nonce, err := hkdf.Expand(sha256.New, prk, "Content-Encoding: nonce\x00", 12)
	if err != nil {
		return nil, err
	}

	block, err := aes.NewCipher(cek)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	// A single record: the payload and the 0x02 "last record" delimiter.
	plain := append(append([]byte{}, payload...), 0x02)

	var out bytes.Buffer
	out.Write(salt)
	_ = binary.Write(&out, binary.BigEndian, uint32(recordSize))
	out.WriteByte(65) // key id length: an uncompressed P-256 point
	out.Write(asPublic)
	out.Write(gcm.Seal(nil, nonce, plain, nil))
	return out.Bytes(), nil
}

// pushHosts are the push services browsers subscribe with. The server
// only ever posts to these, so a subscription cannot point it at an
// internal address.
var pushHosts = []string{
	"fcm.googleapis.com",                // Chrome, Edge on Android, Opera
	"updates.push.services.mozilla.com", // Firefox
	".push.apple.com",                   // Safari
	".notify.windows.com",               // Edge on Windows
}

// AllowedEndpoint reports whether an endpoint is an https URL on a known
// push service.
func AllowedEndpoint(endpoint string) bool {
	u, err := url.Parse(endpoint)
	if err != nil || u.Scheme != "https" || u.User != nil || u.Port() != "" {
		return false
	}
	host := strings.ToLower(u.Hostname())
	for _, h := range pushHosts {
		if host == h || (strings.HasPrefix(h, ".") && strings.HasSuffix(host, h)) {
			return true
		}
	}
	return false
}

// ValidSubscription checks the shapes of a subscription's keys.
func ValidSubscription(sub Subscription) bool {
	p, err := b64.DecodeString(sub.P256dh)
	if err != nil || len(p) != 65 || p[0] != 4 {
		return false
	}
	a, err := b64.DecodeString(sub.Auth)
	return err == nil && len(a) == 16
}

// ErrGone reports a subscription the push service no longer knows (the
// user unsubscribed or the browser dropped it); the caller deletes it.
var ErrGone = errors.New("webpush: subscription is gone")

// Client delivers messages.
type Client struct {
	VAPID *VAPID
	HTTP  *http.Client
	Now   func() time.Time
}

// Options tune one delivery.
type Options struct {
	TTL     time.Duration // how long the push service keeps an undelivered message
	Urgency string        // very-low, low, normal, high; empty means normal
	Topic   string        // replaces an undelivered message with the same topic
}

// Send encrypts and posts a payload to the subscription's endpoint.
func (c *Client) Send(ctx context.Context, sub Subscription, payload []byte, opts Options) error {
	body, err := Encrypt(payload, sub)
	if err != nil {
		return err
	}
	now := time.Now
	if c.Now != nil {
		now = c.Now
	}
	auth, err := c.VAPID.authorization(sub.Endpoint, now())
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, sub.Endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	ttl := opts.TTL
	if ttl <= 0 {
		ttl = 24 * time.Hour
	}
	req.Header.Set("Authorization", auth)
	req.Header.Set("Content-Encoding", "aes128gcm")
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("TTL", strconv.Itoa(int(ttl.Seconds())))
	if opts.Urgency != "" {
		req.Header.Set("Urgency", opts.Urgency)
	}
	if opts.Topic != "" {
		req.Header.Set("Topic", opts.Topic)
	}
	hc := c.HTTP
	if hc == nil {
		hc = http.DefaultClient
	}
	resp, err := hc.Do(req)
	if err != nil {
		return fmt.Errorf("webpush: deliver: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	switch {
	case resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusGone:
		return ErrGone
	case resp.StatusCode >= 300:
		return fmt.Errorf("webpush: push service answered %s", resp.Status)
	}
	return nil
}
