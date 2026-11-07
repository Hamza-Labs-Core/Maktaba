package direct

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"syscall"
	"time"
)

// Results of the outside check (story 50.15 §2), as the home's settings word them.
const (
	CheckOK        = "ok"
	CheckClosed    = "closed"
	CheckTimeout   = "timeout"
	CheckWrongHome = "wrong_home"
	CheckCGNAT     = "cgnat"
)

// ServerIDHeader is how the home's direct listener says which home it is: /api/health answers
// it with the home's cloud server_id.
const ServerIDHeader = "X-Maktaba-Server-Id"

// Shared says whether ip can't be reached from outside: carrier-grade NAT (100.64.0.0/10), a
// private, loopback or link-local address. A router reporting one as its external address
// means the provider shares it, so direct access can't work.
func Shared(ip netip.Addr) bool {
	ip = ip.Unmap()
	return !ip.IsValid() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsUnspecified() ||
		netip.MustParsePrefix("100.64.0.0/10").Contains(ip)
}

// Checker connects to a home from outside it, as an app would.
type Checker struct {
	Roots   *x509.CertPool // nil: the system's (production); the test CA's in the rehearsal
	Timeout time.Duration  // the whole check (10 s)
	// Dial connects to addr; the default dials it directly (tests point a name elsewhere).
	Dial func(ctx context.Context, network, addr string) (net.Conn, error)
}

// Check dials ip:port, expects a certificate valid for name (one of the home's names) and
// /api/health naming serverID. It never follows a redirect.
func (c Checker) Check(ctx context.Context, ip netip.Addr, port int, name, serverID string) string {
	timeout := c.Timeout
	if timeout == 0 {
		timeout = 10 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	addr := net.JoinHostPort(ip.Unmap().String(), strconv.Itoa(port))
	dial := c.Dial
	if dial == nil {
		dial = (&net.Dialer{}).DialContext
	}
	raw, err := dial(ctx, "tcp", addr)
	if err != nil {
		return dialResult(err)
	}
	conn := tls.Client(raw, &tls.Config{ServerName: name, RootCAs: c.Roots, MinVersion: tls.VersionTLS12})
	if err := conn.HandshakeContext(ctx); err != nil {
		_ = raw.Close()
		if isTimeout(err) {
			return CheckTimeout
		}
		return CheckWrongHome // something answers, but not with this home's certificate
	}
	used := false
	client := &http.Client{
		Transport: &http.Transport{
			DialTLSContext: func(context.Context, string, string) (net.Conn, error) {
				if used {
					return nil, errors.New("one connection")
				}
				used = true
				return conn, nil
			},
		},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	defer client.CloseIdleConnections()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "https://"+net.JoinHostPort(name, strconv.Itoa(port))+"/api/health", nil)
	resp, err := client.Do(req)
	if err != nil {
		if isTimeout(err) {
			return CheckTimeout
		}
		return CheckWrongHome
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK || resp.Header.Get(ServerIDHeader) != serverID {
		return CheckWrongHome
	}
	return CheckOK
}

func dialResult(err error) string {
	if isTimeout(err) {
		return CheckTimeout
	}
	if errors.Is(err, syscall.ECONNREFUSED) {
		return CheckClosed
	}
	var op *net.OpError
	if errors.As(err, &op) {
		return CheckClosed
	}
	return CheckTimeout
}

func isTimeout(err error) bool {
	var ne net.Error
	return errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &ne) && ne.Timeout())
}
