package plugins

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/serversettings"
)

// SupervisorHTTP is this home's supervisor's loopback plugin routes (POST /plugins/install,
// POST /plugins/{id}/discard), behind its per-boot token.
type SupervisorHTTP struct {
	Sup *serversettings.Supervisor
}

// SupervisorFromEnv is the supervisor this API runs under (nil: none).
func SupervisorFromEnv() *SupervisorHTTP {
	if s := serversettings.SupervisorFromEnv(); s != nil {
		return &SupervisorHTTP{Sup: s}
	}
	return nil
}

type supAnswer struct {
	Installed
	Error  string `json:"error"`
	Detail string `json:"detail"`
	Reason string `json:"reason"`
	Step   string `json:"step"`
}

// InstallPackage streams a package to the supervisor; a refusal is a *Problem with the
// supervisor's code (a manifest, package or host load code, busy, disk_space).
func (s *SupervisorHTTP) InstallPackage(ctx context.Context, r io.Reader) (*Installed, error) {
	// no 10 s timeout: the host's preload compiles the component
	sup := *s.Sup
	sup.HTTP = &http.Client{}
	if s.Sup.HTTP != nil {
		sup.HTTP.Transport = s.Sup.HTTP.Transport
	}
	resp, err := sup.Do(ctx, http.MethodPost, "/plugins/install", r, "application/octet-stream")
	if err != nil {
		return nil, problem(http.StatusServiceUnavailable, CodeNoSupervisor, "%v", err)
	}
	defer resp.Body.Close()
	var a supAnswer
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err := json.Unmarshal(raw, &a); err != nil {
		return nil, problem(http.StatusBadGateway, CodeNoSupervisor, "the supervisor answered %d: %s", resp.StatusCode,
			strings.TrimSpace(string(raw)))
	}
	if resp.StatusCode != http.StatusOK {
		status := resp.StatusCode
		if status == http.StatusNotFound || status == http.StatusForbidden {
			// a supervisor without the plugin routes (no plugins on this machine)
			return nil, problem(http.StatusServiceUnavailable, CodeNoSupervisor, "this machine runs no plugins")
		}
		p := problem(status, a.Error, "%s", a.Detail)
		if a.Reason != "" || a.Step != "" {
			p.Params = map[string]any{}
			if a.Reason != "" {
				p.Params["reason"] = a.Reason
			}
			if a.Step != "" {
				// the chain's step a signature refusal names (story 54.14 §3)
				p.Params["step"] = a.Step
			}
		}
		return nil, p
	}
	return &a.Installed, nil
}

// Discard asks the supervisor to remove a folder it placed for an install the API refused.
func (s *SupervisorHTTP) Discard(ctx context.Context, id, folder string) error {
	resp, err := s.Sup.Do(ctx, http.MethodPost, "/plugins/"+url.PathEscape(id)+"/discard?folder="+url.QueryEscape(folder), nil, "")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("the supervisor answered %d", resp.StatusCode)
	}
	return nil
}
