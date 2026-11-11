package main

// Epic 59 story 59.13: setup's share step on a NAS home. A node opens the NAS's files over
// SMB (/Volumes/Movies on a Mac), the home has them as its own folders (/volume1/Movies),
// and the two match when the home knows which share each folder is. The libraries step
// proposes one per root (shareProposals: the platform's layout, else nothing), the
// screens ask the household to confirm or type it (or mark the folder as not shared), and
// the `shares` step keeps the answer ([media] shares, share_server, share_aliases at
// finish). The pipeline puts it in the NAS's storage server record (MAKTABA_HOME_SHARES).

import (
	"encoding/json"
	"net"
	"os"
	"sort"
	"strings"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/hostinfo"
	"github.com/Hamza-Labs-Core/Maktaba/shared/go/mediaaccess"
)

// shareStepPlatform: the share step runs on a NAS's own system, and in a container on
// some other Linux whose household names its shares ("Other" in the story's table).
func shareStepPlatform(platform string) bool {
	return hostinfo.IsNAS(platform) || platform == hostinfo.PlatformDocker
}

// shareProposal is one library root's share as setup worked it out.
type shareProposal struct {
	Path string `json:"path"`
	// Share is the share the platform's layout gives the root ("" none: the screens ask).
	Share string `json:"share"`
	// Folder is the share's own folder (the root, or a folder above it).
	Folder string `json:"folder"`
	// HostPath: the root is where the NAS itself keeps its shares (false: a mount at
	// another path, such as the old /media catch-all).
	HostPath bool `json:"host_path"`
}

// shareStep is the libraries step's answer's `shares` on a NAS home.
type shareStep struct {
	Platform string          `json:"platform"`
	Roots    []shareProposal `json:"roots"`
	// Servers are the names the NAS may be mounted by (its host name, its mDNS name, its
	// addresses): the first is suggested as the record's name, all are kept as aliases.
	Servers []string `json:"servers"`
}

// hostNames are the names nodes may mount this machine by (tests replace it).
var hostNames = func() []string {
	var out []string
	add := func(n string) {
		n = strings.TrimSpace(strings.ToLower(n))
		for _, o := range out {
			if o == n {
				return
			}
		}
		if n != "" {
			out = append(out, n)
		}
	}
	if h, err := os.Hostname(); err == nil {
		short, _, _ := strings.Cut(h, ".")
		add(short)
		add(short + ".local")
	}
	for _, ip := range strings.Split(os.Getenv("MAKTABA_HOST_IP"), ",") {
		add(ip)
	}
	if addrs, err := net.InterfaceAddrs(); err == nil {
		var ips []string
		for _, a := range addrs {
			if n, ok := a.(*net.IPNet); ok && n.IP.To4() != nil && !n.IP.IsLoopback() && !n.IP.IsLinkLocalUnicast() {
				ips = append(ips, n.IP.String())
			}
		}
		sort.Strings(ips)
		for _, ip := range ips {
			add(ip)
		}
	}
	return out
}

// shareProposals is the share step's question for roots on platform (nil off a NAS).
func shareProposals(platform string, roots []string) *shareStep {
	if !shareStepPlatform(platform) {
		return nil
	}
	st := &shareStep{Platform: platform, Roots: []shareProposal{}, Servers: hostNames()}
	if st.Servers == nil {
		st.Servers = []string{}
	}
	for _, r := range roots {
		p := shareProposal{Path: r, Folder: r, HostPath: mediaaccess.IsHostPath(platform, r)}
		if share, folder, ok := mediaaccess.RuleShare(platform, r); ok {
			p.Share, p.Folder = share, folder
		}
		st.Roots = append(st.Roots, p)
	}
	return st
}

// stepShares: the `shares` step (story 59.13). Input {server, aliases, shares: [{path,
// share}]}: the NAS's name, the names nodes mount it by, and each folder's SMB share as
// the household confirmed it ("" when it is not shared on the network: nodes cannot work
// on it). Errors share_server (field server), share_bad (field shares).
func stepShares(s *setupRun, in json.RawMessage) (any, error) {
	var req struct {
		Server  string   `json:"server"`
		Aliases []string `json:"aliases"`
		Shares  []struct {
			Path  string `json:"path"`
			Share string `json:"share"`
		} `json:"shares"`
	}
	if err := decodeInput(in, &req); err != nil {
		return nil, err
	}
	server := strings.ToLower(strings.TrimSpace(req.Server))
	server = strings.TrimSuffix(server, ".local")
	if server == "" || strings.ContainsAny(server, `/\ `) || len(server) > 253 {
		return nil, failStep("share_server", "server", "Give the NAS's name as your other computers see it (for example diskstation).")
	}
	shares := map[string]string{}
	for i, e := range req.Shares {
		path, share := strings.TrimRight(strings.TrimSpace(e.Path), "/"), strings.TrimSpace(e.Share)
		line := i + 1
		if !strings.HasPrefix(path, "/") || strings.ContainsAny(share, `/\`) {
			return nil, failStep("share_bad", "shares", "Line %d: a share is a name without slashes, for a whole folder path.", line).
				with("line", line, "path", path)
		}
		shares[path] = share
	}
	var aliases []string
	seen := map[string]bool{server: true}
	for _, a := range req.Aliases {
		a = strings.TrimSpace(a)
		if a != "" && !seen[strings.ToLower(a)] && len(aliases) < 32 {
			seen[strings.ToLower(a)] = true
			aliases = append(aliases, a)
		}
	}
	if err := s.updatePending(func(p *setupPending) {
		p.Shares, p.ShareServer, p.ShareAliases = shares, server, aliases
	}); err != nil {
		return nil, err
	}
	return map[string]any{"server": server, "aliases": aliases, "shares": shares}, nil
}
