// Package netguard keeps server-side fetches of user-supplied links out of
// the private network: loopback, RFC 1918, CGNAT, link-local (cloud
// metadata), unique-local and other addresses that are not on the public
// internet. CheckURL vets a link before it is handed to another program;
// Control and Transport enforce the same rule at connect time for our own
// HTTP clients, which covers redirects and DNS rebinding.
package netguard

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// ErrPrivateAddress means a host is, or resolves to, an address that is
// not on the public internet.
var ErrPrivateAddress = errors.New("netguard: private or local address")

// Resolver looks up a host name; *net.Resolver implements it.
type Resolver interface {
	LookupNetIP(ctx context.Context, network, host string) ([]netip.Addr, error)
}

// nonPublic lists the ranges the netip predicates do not cover.
var nonPublic = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),       // "this network"; 0.0.0.0 reaches the local host
	netip.MustParsePrefix("100.64.0.0/10"),   // carrier-grade NAT
	netip.MustParsePrefix("192.0.0.0/24"),    // IETF protocol assignments
	netip.MustParsePrefix("192.0.2.0/24"),    // documentation
	netip.MustParsePrefix("198.18.0.0/15"),   // benchmarking
	netip.MustParsePrefix("198.51.100.0/24"), // documentation
	netip.MustParsePrefix("203.0.113.0/24"),  // documentation
	netip.MustParsePrefix("240.0.0.0/4"),     // reserved, broadcast
	netip.MustParsePrefix("::/96"),           // IPv4-compatible (deprecated)
	netip.MustParsePrefix("64:ff9b:1::/48"),  // local-use NAT64
	netip.MustParsePrefix("100::/64"),        // discard-only
	netip.MustParsePrefix("2001:db8::/32"),   // documentation
	netip.MustParsePrefix("fec0::/10"),       // site-local (deprecated)
}

// Prefixes whose addresses embed an IPv4 address that a gateway would
// reach on our behalf; the embedded address is judged instead.
var (
	nat64     = netip.MustParsePrefix("64:ff9b::/96")
	sixToFour = netip.MustParsePrefix("2002::/16")
)

// IsPublicIP reports whether ip is a unicast address on the public
// internet. IPv4-mapped IPv6 addresses are judged as IPv4.
func IsPublicIP(ip net.IP) bool {
	a, ok := netip.AddrFromSlice(ip)
	return ok && IsPublicAddr(a)
}

// IsPublicAddr is IsPublicIP for netip.Addr.
func IsPublicAddr(a netip.Addr) bool {
	a = a.WithZone("").Unmap()
	if !a.IsValid() || a.IsUnspecified() || a.IsLoopback() || a.IsPrivate() ||
		a.IsLinkLocalUnicast() || a.IsMulticast() {
		return false
	}
	for _, p := range nonPublic {
		if p.Contains(a) {
			return false
		}
	}
	if nat64.Contains(a) {
		b := a.As16()
		return IsPublicAddr(netip.AddrFrom4([4]byte(b[12:16])))
	}
	if sixToFour.Contains(a) {
		b := a.As16()
		return IsPublicAddr(netip.AddrFrom4([4]byte(b[2:6])))
	}
	return true
}

// CheckURL resolves the link's host and returns ErrPrivateAddress (wrapped,
// naming the host) when the host is, or resolves to, any non-public
// address. A nil resolver uses net.DefaultResolver. Lookup failures are
// returned as they are.
func CheckURL(ctx context.Context, resolver Resolver, raw string) error {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return err
	}
	host := u.Hostname()
	if host == "" {
		return errors.New("netguard: url has no host")
	}
	return CheckHost(ctx, resolver, host)
}

// CheckHost is CheckURL for a bare host name or address literal.
func CheckHost(ctx context.Context, resolver Resolver, host string) error {
	if a, ok := literal(host); ok {
		if !IsPublicAddr(a) {
			return fmt.Errorf("%w: %s", ErrPrivateAddress, host)
		}
		return nil
	}
	if resolver == nil {
		resolver = net.DefaultResolver
	}
	addrs, err := resolver.LookupNetIP(ctx, "ip", host)
	if err != nil {
		return err
	}
	if len(addrs) == 0 {
		return fmt.Errorf("netguard: %s has no addresses", host)
	}
	for _, a := range addrs {
		if !IsPublicAddr(a) {
			return fmt.Errorf("%w: %s", ErrPrivateAddress, host)
		}
	}
	return nil
}

// literal parses an address written into the host itself. Besides the
// usual forms it accepts the legacy inet_aton spellings (127.1, 2130706433,
// 0x7f.0.0.1, 0177.0.0.1) that other programs' resolvers honour.
func literal(host string) (netip.Addr, bool) {
	host = strings.TrimSuffix(host, ".")
	if a, err := netip.ParseAddr(host); err == nil {
		return a, true
	}
	parts := strings.Split(host, ".")
	if len(parts) > 4 {
		return netip.Addr{}, false
	}
	nums := make([]uint64, len(parts))
	for i, p := range parts {
		n, err := parseInetAtonPart(p)
		if err != nil {
			return netip.Addr{}, false
		}
		nums[i] = n
	}
	// The last part fills the remaining bytes: a.b.c.d, a.b.cd, a.bcd, abcd.
	last := len(nums) - 1
	if nums[last] >= 1<<(8*(4-last)) {
		return netip.Addr{}, false
	}
	v := nums[last]
	for i := range last {
		if nums[i] > 0xff {
			return netip.Addr{}, false
		}
		v |= nums[i] << (8 * (3 - i))
	}
	return netip.AddrFrom4([4]byte{byte(v >> 24), byte(v >> 16), byte(v >> 8), byte(v)}), true //nolint:gosec // v < 1<<32 by construction
}

// parseInetAtonPart reads one inet_aton component: decimal, 0x hex or
// 0-prefixed octal.
func parseInetAtonPart(p string) (uint64, error) {
	base := 10
	switch {
	case p == "":
		return 0, strconv.ErrSyntax
	case len(p) > 2 && (p[:2] == "0x" || p[:2] == "0X"):
		base, p = 16, p[2:]
	case len(p) > 1 && p[0] == '0':
		base, p = 8, p[1:]
	}
	return strconv.ParseUint(p, base, 32)
}

// Control is a net.Dialer Control function that refuses connections to
// non-public addresses. It sees the address actually dialled, after DNS
// and after every redirect.
func Control(_, address string, _ syscall.RawConn) error {
	ap, err := netip.ParseAddrPort(address)
	if err != nil {
		return fmt.Errorf("netguard: dial %s: %w", address, err)
	}
	if !IsPublicAddr(ap.Addr()) {
		return fmt.Errorf("%w: %s", ErrPrivateAddress, ap.Addr())
	}
	return nil
}

// Transport is an http.Transport whose connections go only to public
// addresses. It uses no proxy: a proxy would fetch on our behalf, beyond
// the reach of the check.
func Transport() *http.Transport {
	t := http.DefaultTransport.(*http.Transport).Clone() //nolint:forcetypeassert // the standard library's own type
	t.Proxy = nil
	d := &net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second, Control: Control}
	t.DialContext = d.DialContext
	return t
}
