package directaccess

import (
	"bufio"
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"html"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// UPnP asks an Internet Gateway Device (UPnP-IGD v1 or v2) on the LAN: it finds the router by
// SSDP, reads its description, and calls WANIPConnection (or WANPPPConnection) by SOAP.
type UPnP struct {
	// SSDP is where the M-SEARCH goes; default the multicast group 239.255.255.250:1900.
	SSDP string
	// Wait is how long answers are awaited; default 2 s.
	Wait   time.Duration
	Client *http.Client

	control, service string     // found once
	local           netip.Addr // this machine's address towards the router
}

func (u *UPnP) Name() string { return "upnp" }

var igdTargets = []string{
	"urn:schemas-upnp-org:device:InternetGatewayDevice:2",
	"urn:schemas-upnp-org:device:InternetGatewayDevice:1",
}

// discover finds the router's control URL and this machine's address towards it.
func (u *UPnP) discover(ctx context.Context) error {
	if u.control != "" {
		return nil
	}
	ssdp := u.SSDP
	if ssdp == "" {
		ssdp = "239.255.255.250:1900"
	}
	wait := u.Wait
	if wait <= 0 {
		wait = 2 * time.Second
	}
	dst, err := net.ResolveUDPAddr("udp4", ssdp)
	if err != nil {
		return err
	}
	conn, err := net.ListenUDP("udp4", nil)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()
	for _, st := range igdTargets {
		msg := "M-SEARCH * HTTP/1.1\r\nHOST: 239.255.255.250:1900\r\nMAN: \"ssdp:discover\"\r\nMX: 2\r\nST: " + st + "\r\n\r\n"
		if _, err := conn.WriteToUDP([]byte(msg), dst); err != nil {
			return err
		}
	}
	deadline := time.Now().Add(wait)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	_ = conn.SetReadDeadline(deadline)
	buf := make([]byte, 2048)
	var errs []error
	for {
		n, _, err := conn.ReadFromUDP(buf)
		if err != nil {
			break
		}
		resp, err := http.ReadResponse(bufio.NewReader(bytes.NewReader(buf[:n])), nil)
		if err != nil {
			continue
		}
		loc := resp.Header.Get("Location")
		_ = resp.Body.Close()
		if loc == "" {
			continue
		}
		if err := u.describe(ctx, loc); err != nil {
			errs = append(errs, err)
			continue
		}
		return nil
	}
	if len(errs) > 0 {
		return errors.Join(errs...)
	}
	return errors.New("upnp: no gateway answered")
}

type upnpDevice struct {
	Services []struct {
		Type    string `xml:"serviceType"`
		Control string `xml:"controlURL"`
	} `xml:"serviceList>service"`
	Devices []upnpDevice `xml:"deviceList>device"`
}

// describe reads the device description at loc and keeps its WAN connection service.
func (u *UPnP) describe(ctx context.Context, loc string) error {
	base, err := url.Parse(loc)
	if err != nil || (base.Scheme != "http" && base.Scheme != "https") {
		return fmt.Errorf("upnp: a bad location %q", loc)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, loc, nil)
	if err != nil {
		return err
	}
	resp, err := u.client().Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	var root struct {
		URLBase string     `xml:"URLBase"`
		Device  upnpDevice `xml:"device"`
	}
	if err := xml.NewDecoder(io.LimitReader(resp.Body, 256<<10)).Decode(&root); err != nil {
		return fmt.Errorf("upnp: the description: %w", err)
	}
	if root.URLBase != "" {
		if b, err := url.Parse(root.URLBase); err == nil {
			base = b
		}
	}
	var find func(d upnpDevice) (string, string)
	find = func(d upnpDevice) (string, string) {
		for _, s := range d.Services {
			if strings.Contains(s.Type, ":WANIPConnection:") || strings.Contains(s.Type, ":WANPPPConnection:") {
				return s.Type, s.Control
			}
		}
		for _, c := range d.Devices {
			if t, ctl := find(c); t != "" {
				return t, ctl
			}
		}
		return "", ""
	}
	typ, ctl := find(root.Device)
	if typ == "" {
		return errors.New("upnp: the gateway has no WAN connection service")
	}
	ref, err := url.Parse(ctl)
	if err != nil {
		return err
	}
	control := base.ResolveReference(ref)
	// this machine's address towards the router: the mapping's internal client
	c, err := net.Dial("udp", control.Host)
	if err != nil {
		hostPort := control.Host
		if control.Port() == "" {
			hostPort = net.JoinHostPort(control.Hostname(), "80")
		}
		if c, err = net.Dial("udp", hostPort); err != nil {
			return err
		}
	}
	local, _ := netip.ParseAddrPort(c.LocalAddr().String())
	_ = c.Close()
	u.control, u.service, u.local = control.String(), typ, local.Addr().Unmap()
	return nil
}

func (u *UPnP) client() *http.Client {
	if u.Client != nil {
		return u.Client
	}
	return &http.Client{Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

// soap calls action with args (in order) and answers the response's fields.
func (u *UPnP) soap(ctx context.Context, action string, args [][2]string) (map[string]string, error) {
	var body strings.Builder
	body.WriteString(`<?xml version="1.0"?><s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/" s:encodingStyle="http://schemas.xmlsoap.org/soap/encoding/"><s:Body>`)
	fmt.Fprintf(&body, `<u:%s xmlns:u="%s">`, action, html.EscapeString(u.service))
	for _, a := range args {
		fmt.Fprintf(&body, "<%s>%s</%s>", a[0], html.EscapeString(a[1]), a[0])
	}
	fmt.Fprintf(&body, `</u:%s></s:Body></s:Envelope>`, action)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.control, strings.NewReader(body.String()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", `text/xml; charset="utf-8"`)
	req.Header.Set("SOAPAction", `"`+u.service+"#"+action+`"`)
	resp, err := u.client().Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	out := map[string]string{}
	dec := xml.NewDecoder(io.LimitReader(resp.Body, 64<<10))
	var name string
	for {
		tok, err := dec.Token()
		if err != nil {
			break
		}
		switch t := tok.(type) {
		case xml.StartElement:
			name = t.Name.Local
		case xml.CharData:
			if name != "" {
				out[name] += string(t)
			}
		case xml.EndElement:
			name = ""
		}
	}
	if resp.StatusCode != http.StatusOK {
		return out, fmt.Errorf("upnp: %s: status %d (error %s %s)", action, resp.StatusCode, out["errorCode"], out["errorDescription"])
	}
	return out, nil
}

// Map adds a TCP mapping from external to this machine's internal port. With external 0 it
// asks for a random port in 30000–49999 (a few tries when one is taken).
func (u *UPnP) Map(ctx context.Context, internal, external int, lifetime time.Duration) (Mapping, error) {
	if err := u.discover(ctx); err != nil {
		return Mapping{}, err
	}
	ports := []int{external}
	if external == 0 {
		ports = randomPorts(4)
	} else {
		ports = append(ports, randomPorts(3)...)
	}
	var err error
	for _, p := range ports {
		_, err = u.soap(ctx, "AddPortMapping", [][2]string{
			{"NewRemoteHost", ""}, {"NewExternalPort", strconv.Itoa(p)}, {"NewProtocol", "TCP"},
			{"NewInternalPort", strconv.Itoa(internal)}, {"NewInternalClient", u.local.String()},
			{"NewEnabled", "1"}, {"NewPortMappingDescription", "Maktaba direct access"},
			{"NewLeaseDuration", strconv.Itoa(int(lifetime / time.Second))},
		})
		if err == nil {
			m := Mapping{By: u.Name(), Internal: internal, External: p, Lifetime: lifetime}
			if r, err := u.soap(ctx, "GetExternalIPAddress", nil); err == nil {
				m.ExternalIP, _ = netip.ParseAddr(strings.TrimSpace(r["NewExternalIPAddress"]))
			}
			return m, nil
		}
	}
	u.control = "" // find the router again next time
	return Mapping{}, err
}

// Unmap deletes the mapping.
func (u *UPnP) Unmap(ctx context.Context, m Mapping) error {
	if err := u.discover(ctx); err != nil {
		return err
	}
	_, err := u.soap(ctx, "DeletePortMapping", [][2]string{
		{"NewRemoteHost", ""}, {"NewExternalPort", strconv.Itoa(m.External)}, {"NewProtocol", "TCP"},
	})
	return err
}
