package ingest

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeResolver answers from a table; unknown hosts fail like DNS does.
type fakeResolver map[string]string

func (f fakeResolver) LookupNetIP(_ context.Context, _, host string) ([]netip.Addr, error) {
	ip, ok := f[host]
	if !ok {
		return nil, &net.DNSError{Err: "no such host", Name: host, IsNotFound: true}
	}
	return []netip.Addr{netip.MustParseAddr(ip)}, nil
}

func TestSourcePolicyCheck(t *testing.T) {
	ctx := context.Background()
	p := SourcePolicy{Resolver: fakeResolver{"cdn.example": "93.184.216.34", "seaweedfs": "172.18.0.3"}}

	assert.NoError(t, p.check(ctx, "url:https://cdn.example/a.mp4", "https://cdn.example/a.mp4"))
	for _, raw := range []string{
		"http://seaweedfs:9000/couchcast/",
		"http://169.254.169.254/latest/meta-data/",
		"http://127.0.0.1:8080/metrics",
		"http://[::1]/",
	} {
		assert.ErrorIs(t, p.check(ctx, "url:"+raw, raw), ErrPrivateAddress, raw)
	}
	err := p.check(ctx, "url:https://nowhere.example/", "https://nowhere.example/")
	assert.ErrorIs(t, err, ErrUnknownHost)
	assert.NotErrorIs(t, err, ErrPrivateAddress)

	// YouTube keys are only minted for YouTube's hosts: no lookup.
	assert.NoError(t, p.check(ctx, "youtube:aqz-KE-bpKQ", "https://www.youtube.com/watch?v=aqz-KE-bpKQ"))

	// Self-hosters may opt in to their LAN.
	open := SourcePolicy{AllowPrivate: true, Resolver: p.Resolver}
	assert.NoError(t, open.check(ctx, "url:http://seaweedfs:9000/", "http://seaweedfs:9000/"))
	assert.NoError(t, open.check(ctx, "url:http://192.168.1.10/film.mkv", "http://192.168.1.10/film.mkv"))
}

func TestThumbnailClientRefusesPrivateAddresses(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = w.Write([]byte("jpegbytes"))
	}))
	defer srv.Close()
	ctx := context.Background()

	// The poster URL comes from the site: a loopback one is refused at
	// connect time, however the name resolved.
	_, err := fetchThumbnail(ctx, SourcePolicy{}.httpClient(thumbnailTimeout), srv.URL+"/poster.jpg", t.TempDir())
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrPrivateAddress)

	name, err := fetchThumbnail(ctx, SourcePolicy{AllowPrivate: true}.httpClient(thumbnailTimeout), srv.URL+"/poster.jpg", t.TempDir())
	require.NoError(t, err)
	assert.Equal(t, "thumb.jpg", name)
}
