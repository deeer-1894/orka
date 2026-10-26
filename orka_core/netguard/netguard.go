// Package netguard decides which addresses outbound requests made on a user's
// behalf may reach. User input (a URL to fetch, a model endpoint, a watched
// page) must not become a way into the deployment's own network, so the
// check happens on every resolved address at dial time, where DNS rebinding
// cannot slip past it.
package netguard

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
)

var blocked = mustNets(
	"0.0.0.0/8",          // "this network"
	"100.64.0.0/10",      // carrier-grade NAT, shared address space
	"192.0.0.0/24",       // IETF protocol assignments
	"198.18.0.0/15",      // benchmarking
	"240.0.0.0/4",        // reserved, broadcast
	"64:ff9b::/96",       // NAT64: an IPv6 spelling of any IPv4 address
	"64:ff9b:1::/48",     // local-use NAT64
	"2001:db8::/32",      // documentation
	"169.254.169.254/32", // cloud metadata (also link-local; named for clarity)
)

// IsPublic reports whether ip is an ordinary internet address: not loopback,
// private, link-local, multicast, unspecified or any reserved range above.
func IsPublic(ip net.IP) bool {
	if v4 := ip.To4(); v4 != nil {
		ip = v4
	}
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsUnspecified() || ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() || ip.IsInterfaceLocalMulticast() || ip.IsMulticast() {
		return false
	}
	for _, n := range blocked {
		if n.Contains(ip) {
			return false
		}
	}
	return true
}

// Policy is the public internet plus networks an administrator allowed.
type Policy struct {
	Allowed []*net.IPNet
}

// FromEnv reads a comma-separated list of CIDRs or single addresses, such as
// ORKA_MODEL_ALLOWED_NETWORKS="10.0.0.0/8,127.0.0.1". Invalid entries are an
// error so a typo does not silently leave a model unreachable.
func FromEnv(name string) (Policy, error) {
	nets, err := ParseNetworks(os.Getenv(name))
	if err != nil {
		return Policy{}, fmt.Errorf("%s: %w", name, err)
	}
	return Policy{Allowed: nets}, nil
}

func ParseNetworks(s string) ([]*net.IPNet, error) {
	var out []*net.IPNet
	for _, f := range strings.Split(s, ",") {
		f = strings.TrimSpace(f)
		if f == "" {
			continue
		}
		if !strings.Contains(f, "/") {
			ip := net.ParseIP(f)
			if ip == nil {
				return nil, fmt.Errorf("not an address or CIDR: %q", f)
			}
			bits := 128
			if ip.To4() != nil {
				ip, bits = ip.To4(), 32
			}
			out = append(out, &net.IPNet{IP: ip, Mask: net.CIDRMask(bits, bits)})
			continue
		}
		_, n, err := net.ParseCIDR(f)
		if err != nil {
			return nil, fmt.Errorf("not an address or CIDR: %q", f)
		}
		out = append(out, n)
	}
	return out, nil
}

func (p Policy) Permits(ip net.IP) bool {
	if IsPublic(ip) {
		return true
	}
	for _, n := range p.Allowed {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

// ErrBlocked is returned for a host that resolves to a refused address.
var ErrBlocked = errors.New("address is not on the public internet")

// CheckHost resolves host and refuses it unless every address is permitted.
func (p Policy) CheckHost(ctx context.Context, host string) ([]net.IP, error) {
	if ip := net.ParseIP(host); ip != nil {
		if !p.Permits(ip) {
			return nil, fmt.Errorf("%w (%s)", ErrBlocked, ip)
		}
		return []net.IP{ip}, nil
	}
	if strings.EqualFold(strings.TrimSuffix(host, "."), "localhost") {
		return nil, fmt.Errorf("%w (%s)", ErrBlocked, host)
	}
	addrs, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, err
	}
	if len(addrs) == 0 {
		return nil, fmt.Errorf("no address for %s", host)
	}
	ips := make([]net.IP, 0, len(addrs))
	for _, a := range addrs {
		if !p.Permits(a.IP) {
			return nil, fmt.Errorf("%w (%s resolves to %s)", ErrBlocked, host, a.IP)
		}
		ips = append(ips, a.IP)
	}
	return ips, nil
}

// DialContext dials only permitted addresses, connecting to the address it
// checked rather than resolving again. Addresses in proxies (host:port of the
// configured HTTP proxy, the administrator's infrastructure) are dialed as is.
func (p *Policy) DialContext(d *net.Dialer, proxies map[string]bool) func(ctx context.Context, network, addr string) (net.Conn, error) {
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(addr)
		if err != nil {
			return nil, err
		}
		if proxies[strings.ToLower(addr)] {
			return d.DialContext(ctx, network, addr)
		}
		ips, err := p.CheckHost(ctx, host)
		if err != nil {
			return nil, err
		}
		var last error
		for _, ip := range ips {
			c, err := d.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
			if err == nil {
				return c, nil
			}
			last = err
		}
		return nil, last
	}
}

// Guard makes t refuse non-permitted destinations. Through a proxy the
// destination is checked before the request is handed to the proxy. The
// policy is read at each dial, so a later change to *p applies.
func (p *Policy) Guard(t *http.Transport) *http.Transport {
	t = t.Clone()
	proxies := map[string]bool{}
	base := t.Proxy
	if base != nil {
		for _, name := range []string{"HTTP_PROXY", "http_proxy", "HTTPS_PROXY", "https_proxy"} {
			if u, err := url.Parse(os.Getenv(name)); err == nil && u.Hostname() != "" {
				port := u.Port()
				if port == "" {
					port = map[string]string{"https": "443"}[u.Scheme]
					if port == "" {
						port = "80"
					}
				}
				proxies[strings.ToLower(net.JoinHostPort(u.Hostname(), port))] = true
			}
		}
		t.Proxy = func(r *http.Request) (*url.URL, error) {
			u, err := base(r)
			if err != nil || u == nil {
				return u, err
			}
			// The proxy resolves the name; refuse only what is known to be
			// internal (a lookup this host cannot make is the proxy's call).
			if _, err := p.CheckHost(r.Context(), r.URL.Hostname()); errors.Is(err, ErrBlocked) {
				return nil, err
			}
			return u, nil
		}
	}
	t.DialContext = p.DialContext(&net.Dialer{}, proxies)
	return t
}

func mustNets(cidrs ...string) []*net.IPNet {
	out := make([]*net.IPNet, len(cidrs))
	for i, c := range cidrs {
		_, n, err := net.ParseCIDR(c)
		if err != nil {
			panic(err)
		}
		out[i] = n
	}
	return out
}
