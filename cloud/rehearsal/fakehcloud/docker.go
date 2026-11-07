package fakehcloud

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"time"
)

// DockerMover moves Alias on Network to the container of a server, through
// the Docker Engine API on its unix socket: disconnect the alias's holder,
// then connect the new one with the alias (and its fixed address, so the
// relay's peers still find it).
type DockerMover struct {
	Socket  string           // /var/run/docker.sock
	Network string           // the compose network standing in for the private network
	Alias   string           // relay.test
	Servers map[int64]string // server id -> container name
	// IPs: each container's address on Network (kept when it is reconnected)
	IPs map[int64]string

	client *http.Client
}

func (d *DockerMover) http() *http.Client {
	if d.client == nil {
		d.client = &http.Client{Timeout: 30 * time.Second, Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, "unix", d.Socket)
			},
		}}
	}
	return d.client
}

func (d *DockerMover) post(path string, body any) error {
	b, _ := json.Marshal(body)
	resp, err := d.http().Post("http://docker"+path, "application/json", bytes.NewReader(b))
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 300 {
		msg, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("docker %s: %d %s", path, resp.StatusCode, msg)
	}
	return nil
}

// Move gives the alias to server's container: every container is
// disconnected and reconnected without the alias, the holder with it.
func (d *DockerMover) Move(server int64) error {
	target, ok := d.Servers[server]
	if !ok {
		return fmt.Errorf("fakehcloud: unknown server %d", server)
	}
	nw := "/networks/" + url.PathEscape(d.Network)
	for id, name := range d.Servers {
		_ = d.post(nw+"/disconnect", map[string]any{"Container": name, "Force": true})
		cfg := map[string]any{}
		if ip := d.IPs[id]; ip != "" {
			cfg["IPAMConfig"] = map[string]string{"IPv4Address": ip}
		}
		if name == target {
			cfg["Aliases"] = []string{d.Alias}
		}
		if err := d.post(nw+"/connect", map[string]any{"Container": name, "EndpointConfig": cfg}); err != nil && name == target {
			return err
		}
	}
	return nil
}
