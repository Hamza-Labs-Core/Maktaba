package cluster

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strings"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/common"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/sharepath"
)

// Epic 59 story 59.13: a NAS as the home. Its pipeline keeps a storage server record for
// the NAS itself (its names, and each of its own folders' SMB share in exports) and
// reports the record's name in the home's storage check (home_server). These routes show
// and change that record as one thing:
//
//	GET /api/cluster/storage/home-shares   admin: the NAS's shares, the folders still to name
//	PUT /api/cluster/storage/home-shares   admin: {server?, addresses, shares: [{path, share}]}
//
// A share "" is a folder that is not shared on the network (local only).

// NoWritableShare is the notice code (cluster.no_writable_share in the apps) when no
// shared folder of a NAS home is writable for Maktaba: the cluster's folder and the
// model copy then stay on the home, and nodes download speech models themselves.
const NoWritableShare = "no_writable_share"

// NoWritableShareText is its English (the join's terminal prints it).
const NoWritableShareText = "No shared folder on this NAS is writable for Maktaba, so nodes will download speech " +
	"models themselves. Give Maktaba Read/Write on one shared folder to share them."

// HomeShare is one of the NAS's own folders and its share.
type HomeShare struct {
	Path  string `json:"path"`
	Share string `json:"share"`
	// LocalOnly: not shared on the network; no node can work on it.
	LocalOnly bool `json:"local_only"`
	// HostPath: the NAS's own path for the share (false: a mount at another path, which
	// nodes match by the name given; Settings says so).
	HostPath bool `json:"host_path"`
	// Rule is the share the platform's layout gives the folder ("" none).
	Rule string `json:"rule,omitempty"`
}

// HomeShares is the answer of the home-shares routes.
type HomeShares struct {
	// NAS: the home's pipeline keeps a NAS record (a NAS home, or a container whose
	// household names its shares).
	NAS       bool        `json:"nas"`
	Platform  string      `json:"platform"`
	Server    string      `json:"server"`
	Addresses []string    `json:"addresses"`
	Shares    []HomeShare `json:"shares"`
	// Unnamed are library roots with no share and no rule: setup asks for them.
	Unnamed []string `json:"unnamed"`
	// OtherPath: some share is mounted at a path other than the NAS's own.
	OtherPath bool `json:"other_path"`
	// WritableShare: a shared library folder is writable for Maktaba; false on a NAS home
	// is the notice NoWritableShare.
	WritableShare bool `json:"writable_share"`
	// Notices are codes the apps translate (cluster.<code>).
	Notices []string `json:"notices"`
}

// HomeSharesRequest is the body of PUT /api/cluster/storage/home-shares.
type HomeSharesRequest struct {
	Server    string   `json:"server"`
	Addresses []string `json:"addresses"`
	Shares    []struct {
		Path  string `json:"path"`
		Share string `json:"share"`
	} `json:"shares"`
}

// maxHomeShares bounds the shares one request names.
const maxHomeShares = 256

type homeReport struct {
	HomeServer   string   `json:"home_server"`
	HomePlatform string   `json:"home_platform"`
	Unnamed      []string `json:"unnamed_roots"`
}

func (h *Handler) homeReport(ctx context.Context) (homeReport, error) {
	var rep homeReport
	st, _, err := h.Storage.HomeStorage(ctx)
	if err != nil || st == nil {
		return rep, err
	}
	_ = json.Unmarshal(st, &rep)
	return rep, nil
}

// homeShares builds the answer from the home's report, the NAS's record and the
// libraries' access.
func (h *Handler) homeShares(ctx context.Context) (HomeShares, error) {
	out := HomeShares{Addresses: []string{}, Shares: []HomeShare{}, Unnamed: []string{}, Notices: []string{}}
	rep, err := h.homeReport(ctx)
	if err != nil {
		return out, err
	}
	if rep.HomeServer == "" {
		return out, nil
	}
	out.NAS, out.Platform, out.Server = true, rep.HomePlatform, rep.HomeServer
	servers, err := h.Storage.Servers(ctx)
	if err != nil {
		return out, err
	}
	var rec *cluster.StorageServer
	for i := range servers {
		if servers[i].Name == rep.HomeServer {
			rec = &servers[i]
		}
	}
	if rec != nil {
		out.Addresses = append(out.Addresses, rec.Addresses...)
		for path, share := range rec.Exports {
			hs := HomeShare{Path: path, Share: share, LocalOnly: strings.TrimSpace(share) == "",
				HostPath: sharepath.IsHostPath(rep.HomePlatform, path)}
			if s, _, ok := sharepath.RuleShare(rep.HomePlatform, path); ok {
				hs.Rule = s
			}
			out.OtherPath = out.OtherPath || (!hs.LocalOnly && !hs.HostPath)
			out.Shares = append(out.Shares, hs)
		}
		sort.Slice(out.Shares, func(i, j int) bool { return out.Shares[i].Path < out.Shares[j].Path })
	}
	if rep.Unnamed != nil {
		out.Unnamed = rep.Unnamed
	}
	access, err := h.Storage.LibraryAccess(ctx)
	if err != nil {
		return out, err
	}
	out.WritableShare = writableShare(rep.HomeServer, out.Shares, access)
	if !out.WritableShare {
		out.Notices = append(out.Notices, NoWritableShare)
	}
	return out, nil
}

// writableShare: a library root on one of the NAS's shares (its canonical path, or a
// folder a share names) whose library Maktaba may write (rw, or never checked: how
// Maktaba behaved before story 59.03).
func writableShare(server string, shares []HomeShare, roots []cluster.RootAccess) bool {
	prefix := "smb://" + strings.ToLower(server) + "/"
	for _, r := range roots {
		if r.Access != nil && *r.Access != "rw" {
			continue
		}
		p := strings.ToLower(strings.ReplaceAll(r.Path, `\`, "/"))
		if strings.HasPrefix(p, prefix) {
			return true
		}
		for _, s := range shares {
			sp := strings.ToLower(strings.TrimRight(s.Path, "/"))
			if !s.LocalOnly && (p == sp || strings.HasPrefix(p, sp+"/")) {
				return true
			}
		}
	}
	return false
}

// GetHomeShares implements GET /api/cluster/storage/home-shares.
func (h *Handler) GetHomeShares(w http.ResponseWriter, r *http.Request) {
	if !requireAdmin(w, r) || !h.storageOr404(w, r) {
		return
	}
	out, err := h.homeShares(r.Context())
	if err != nil {
		errInternal(w, r, "read the NAS's shares", err)
		return
	}
	common.WriteJSON(w, r, http.StatusOK, out)
}

// PutHomeShares implements PUT /api/cluster/storage/home-shares: the NAS's names and
// each folder's share, replacing the record's (the share step, Settings). The home's
// pipeline converts the folders' paths at its next storage check.
func (h *Handler) PutHomeShares(w http.ResponseWriter, r *http.Request) {
	if !requireAdmin(w, r) || !h.storageOr404(w, r) {
		return
	}
	var req HomeSharesRequest
	if e := common.ReadJSON(r, &req, 0); e != nil {
		httperror.Write(w, r, e)
		return
	}
	rep, err := h.homeReport(r.Context())
	if err != nil {
		errInternal(w, r, "read the home's storage", err)
		return
	}
	// the record the home's pipeline reports is the NAS (a rename would leave it behind);
	// the body's name is for a home that has not reported one yet
	name := rep.HomeServer
	if name == "" {
		name = strings.ToLower(strings.TrimSpace(req.Server))
	}
	if name == "" {
		httperror.Write(w, r, problem(http.StatusConflict, "not_a_nas_home", "not a NAS home",
			"this home keeps no record of its own shares; name the server"))
		return
	}
	if len(req.Addresses) > maxServerAddresses || len(req.Shares) > maxHomeShares {
		httperror.Write(w, r, httperror.BadRequest("too many addresses or shares"))
		return
	}
	exports := map[string]string{}
	for _, s := range req.Shares {
		path, share := strings.TrimSpace(s.Path), strings.TrimSpace(s.Share)
		if !strings.HasPrefix(path, "/") || strings.Contains(share, "/") || strings.Contains(share, `\`) {
			httperror.Write(w, r, httperror.BadRequest(`shares: [{"path": "/folder", "share": "Name" or "" (local only)}]`))
			return
		}
		exports[strings.TrimRight(path, "/")] = share
	}
	var charMap string
	if servers, err := h.Storage.Servers(r.Context()); err == nil {
		for _, s := range servers {
			if s.Name == name {
				charMap = s.CharMap
			}
		}
	}
	_, err = h.Storage.PutServer(r.Context(), cluster.StorageServer{
		Name: name, Addresses: req.Addresses, CharMap: charMap, Exports: exports,
	})
	if errors.Is(err, cluster.ErrBadServer) {
		httperror.Write(w, r, httperror.BadRequest(err.Error()))
		return
	}
	if err != nil {
		errInternal(w, r, "save the NAS's shares", err)
		return
	}
	out, err := h.homeShares(r.Context())
	if err != nil {
		errInternal(w, r, "read the NAS's shares", err)
		return
	}
	common.WriteJSON(w, r, http.StatusOK, out)
}

// joinNotices are what a machine asking to join should know about this home: on a NAS
// home with no writable shared folder, nodes download their models themselves.
func (h *Handler) joinNotices(ctx context.Context) []JoinNotice {
	if h.Storage == nil {
		return nil
	}
	hs, err := h.homeShares(ctx)
	if err != nil || !hs.NAS || hs.WritableShare {
		return nil
	}
	return []JoinNotice{{Code: NoWritableShare, Message: NoWritableShareText}}
}
