package netguard

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIsPublicIP(t *testing.T) {
	cases := map[string]bool{
		"8.8.8.8":              true,
		"93.184.216.34":        true,
		"2606:4700:4700::1111": true,
		"64:ff9b::808:808":     true, // NAT64 of 8.8.8.8
		"2002:808:808::1":      true, // 6to4 of 8.8.8.8

		"127.0.0.1":              false,
		"127.8.9.10":             false,
		"10.1.2.3":               false,
		"172.16.0.1":             false,
		"172.31.255.255":         false,
		"192.168.1.1":            false,
		"100.64.0.1":             false,
		"100.127.255.255":        false,
		"169.254.169.254":        false,
		"0.0.0.0":                false,
		"0.1.2.3":                false,
		"224.0.0.1":              false,
		"255.255.255.255":        false,
		"198.18.0.1":             false,
		"192.0.2.1":              false,
		"::":                     false,
		"::1":                    false,
		"fe80::1":                false,
		"fc00::1":                false,
		"fd12:3456::1":           false,
		"ff02::1":                false,
		"fec0::1":                false,
		"::ffff:127.0.0.1":       false,
		"::ffff:10.0.0.1":        false,
		"::ffff:169.254.169.254": false,
		"::127.0.0.1":            false,
		"64:ff9b::a00:1":         false, // NAT64 of 10.0.0.1
		"64:ff9b:1::1":           false,
		"2002:7f00:1::1":         false, // 6to4 of 127.0.0.1
		"2001:db8::1":            false,
	}
	for in, want := range cases {
		assert.Equal(t, want, IsPublicIP(net.ParseIP(in)), in)
	}
	assert.False(t, IsPublicIP(nil))
	assert.False(t, IsPublicIP(net.IP{1, 2, 3}))
	assert.False(t, IsPublicAddr(netip.MustParseAddr("fe80::1%eth0")))
}

// fakeResolver answers from a table; unknown hosts fail like DNS does.
type fakeResolver map[string][]string

func (f fakeResolver) LookupNetIP(_ context.Context, _, host string) ([]netip.Addr, error) {
	ips, ok := f[host]
	if !ok {
		return nil, &net.DNSError{Err: "no such host", Name: host, IsNotFound: true}
	}
	out := make([]netip.Addr, 0, len(ips))
	for _, s := range ips {
		out = append(out, netip.MustParseAddr(s))
	}
	return out, nil
}

func TestCheckURL(t *testing.T) {
	r := fakeResolver{
		"cdn.example":    {"93.184.216.34", "2606:2800:220:1::1"},
		"seaweedfs":      {"172.18.0.3"},
		"localhost":      {"127.0.0.1", "::1"},
		"mixed.example":  {"93.184.216.34", "10.0.0.7"}, // one bad answer is enough
		"empty.example":  {},
		"metadata.cloud": {"169.254.169.254"},
	}
	ctx := context.Background()

	for _, ok := range []string{
		"https://cdn.example/video.mp4",
		"http://cdn.example:8080/x",
		"https://8.8.8.8/x",
		"https://[2606:4700:4700::1111]/x",
	} {
		assert.NoError(t, CheckURL(ctx, r, ok), ok)
	}

	for _, private := range []string{
		"http://seaweedfs:9000/couchcast/",
		"http://localhost:8080/metrics",
		"http://mixed.example/",
		"http://metadata.cloud/latest/meta-data/",
		"http://169.254.169.254/latest/meta-data/",
		"http://127.0.0.1:5432/",
		"http://127.1/",
		"http://2130706433/",
		"http://0x7f.0.0.1/",
		"http://0177.0.0.1/",
		"http://0/",
		"http://10.0.0.1./",
		"http://[::1]/",
		"http://[::ffff:127.0.0.1]/",
		"http://[fe80::1%25eth0]/",
		"http://user:pass@192.168.0.1/",
	} {
		err := CheckURL(ctx, r, private)
		assert.ErrorIs(t, err, ErrPrivateAddress, private)
	}

	err := CheckURL(ctx, r, "https://nowhere.example/")
	require.Error(t, err)
	assert.NotErrorIs(t, err, ErrPrivateAddress, "a lookup failure is not a private address")
	var dnsErr *net.DNSError
	assert.True(t, errors.As(err, &dnsErr))

	assert.Error(t, CheckURL(ctx, r, "https://empty.example/"))
	assert.Error(t, CheckURL(ctx, r, "/relative"))
	assert.Error(t, CheckURL(ctx, r, "http://%zz/"))
}

func TestLiteral(t *testing.T) {
	cases := map[string]string{
		"127.1":         "127.0.0.1",
		"10.1.2":        "10.1.0.2",
		"2130706433":    "127.0.0.1",
		"0x7f000001":    "127.0.0.1",
		"0300.0250.1.1": "192.168.1.1",
		"8.8.8.8.":      "8.8.8.8",
	}
	for in, want := range cases {
		a, ok := literal(in)
		require.True(t, ok, in)
		assert.Equal(t, want, a.String(), in)
	}
	for _, name := range []string{"cafe.be", "example.com", "1.2.3.4.5", "256.1.1.1", "1.2.65536", "4294967296", "08.1.1.1", ""} {
		_, ok := literal(name)
		assert.False(t, ok, name)
	}
}

func TestControl(t *testing.T) {
	assert.NoError(t, Control("tcp4", "8.8.8.8:443", nil))
	assert.NoError(t, Control("tcp6", "[2606:4700:4700::1111]:443", nil))
	for _, addr := range []string{"127.0.0.1:80", "[::1]:80", "169.254.169.254:80", "10.0.0.1:5432", "[::ffff:192.168.1.1]:80", "[fe80::1%eth0]:80"} {
		assert.ErrorIs(t, Control("tcp", addr, nil), ErrPrivateAddress, addr)
	}
	assert.Error(t, Control("tcp", "not-an-address", nil))
}

func TestTransportRefusesLoopback(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("internal"))
	}))
	defer srv.Close()

	client := &http.Client{Transport: Transport()}
	resp, err := client.Get(srv.URL) //nolint:noctx // test
	if resp != nil {
		_ = resp.Body.Close()
	}
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrPrivateAddress)

	// A public page redirecting inward is caught at connect time too.
	redirect := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host == "public.example" {
			return &http.Response{StatusCode: http.StatusFound, Header: http.Header{"Location": {srv.URL}}, Body: http.NoBody, Request: r}, nil
		}
		return Transport().RoundTrip(r)
	})}
	resp, err = redirect.Get("http://public.example/") //nolint:noctx // test
	if resp != nil {
		_ = resp.Body.Close()
	}
	assert.ErrorIs(t, err, ErrPrivateAddress)
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
