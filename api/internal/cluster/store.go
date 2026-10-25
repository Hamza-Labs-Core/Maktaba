package cluster

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

// Node states as stored (cluster_nodes.state).
const (
	StatePending  = "pending"
	StateApproved = "approved"
	StateRejected = "rejected"
	StateRevoked  = "revoked"
)

// Path map sources (cluster_nodes.path_map_source).
const (
	PathMapInferred = "inferred"
	PathMapAdmin    = "admin"
)

// ErrNotFound is returned for a missing node.
var ErrNotFound = errors.New("cluster: not found")

// Identity is the instance_identity singleton.
type Identity struct {
	InstanceID       string
	Name             string
	AcceptJoins      bool
	AcceptJoinsUntil *time.Time
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

// Short is the first 8 hex digits of the instance id (the mDNS "id").
func (i Identity) Short() string {
	h := RoleName(i.InstanceID)[len("maktaba_node_"):]
	if len(h) > 8 {
		h = h[:8]
	}
	return h
}

// Node is one cluster_nodes row, secrets included: callers that render it
// must not copy HomePrivkey / Sealed out.
type Node struct {
	ID            string
	Name          string
	State         string
	OS            *string
	Arch          *string
	Versions      json.RawMessage
	Capabilities  json.RawMessage
	Storage       json.RawMessage
	PathMap       json.RawMessage // nil: not set
	PathMapSource *string
	Drain         bool
	NodePubkey    string
	HomePubkey    *string
	HomePrivkey   *string
	CodeAttempts  int
	ExpiresAt     *time.Time
	Sealed        json.RawMessage // nil: none
	DBRole        *string
	RemoteAddr    *string
	APIURL        *string
	CreatedAt     time.Time
	ApprovedAt    *time.Time
	ApprovedBy    *string
	RevokedAt     *time.Time
	LastSeenAt    *time.Time
	UpdatedAt     time.Time

	// What a full node reports about itself (Epic 35 slot 0135, story 35.8): its
	// release, platform, own web address, whether it runs maktaba-server in node mode,
	// the VOD fallback (design §11.5) and the rollout's view of it (story 35.12).
	Version  *string
	Platform *string
	WebURL   *string
	FullNode bool
	// FullCapable: the machine reports it can be a full machine; FullAllowed: a home
	// admin allowed it (slot 0151). FullNode is both: only then does it get a copy.
	FullCapable bool
	FullAllowed bool
	VOD         json.RawMessage
	UpdateState json.RawMessage
	// Copy is what a full node reports of its copy of this database (Epic 37, slot
	// 0147: state, lag, when it last applied), and when it did.
	Copy           json.RawMessage
	CopyReportedAt *time.Time
}

// NodeWorker is a workers row that belongs to a node.
type NodeWorker struct {
	ID         string
	NodeID     string
	Host       string
	Stages     []string
	StagesOff  json.RawMessage
	LastSeenAt time.Time
}

// NodePatch is an admin edit; nil fields are left alone. ClearPathMap
// drops the map (back to "inferred by the node").
type NodePatch struct {
	Name         *string
	Drain        *bool
	PathMap      json.RawMessage
	ClearPathMap bool
	// FullAllowed: a home admin allows (or takes back) a full machine's copy; full_node
	// becomes what the machine reports and this (slot 0151).
	FullAllowed *bool
}

// Store persists the identity and the machine registry.
type Store interface {
	// EnsureIdentity creates the singleton (random instance id, the given
	// name) when absent and returns it.
	EnsureIdentity(ctx context.Context, name string) (Identity, error)
	Identity(ctx context.Context) (Identity, error)
	// SetAcceptJoins switches joins; until is when they turn off again.
	SetAcceptJoins(ctx context.Context, on bool, until *time.Time) (Identity, error)
	// CloseExpiredJoins turns accept_joins off when its time is up and
	// reports whether it did.
	CloseExpiredJoins(ctx context.Context, now time.Time) (bool, error)
	SetName(ctx context.Context, name string) (Identity, error)

	// Node returns ErrNotFound when absent.
	Node(ctx context.Context, id string) (Node, error)
	Nodes(ctx context.Context) ([]Node, error)
	// PutPending creates a pending request, or replaces a pending /
	// rejected one with the same id (new keys, attempts reset).
	PutPending(ctx context.Context, n Node) error
	// WrongCode counts a wrong code; at max attempts the request is
	// rejected. Returns the attempts made.
	WrongCode(ctx context.Context, id string, maxAttempts int, now time.Time) (int, error)
	// Approve stores the role and the sealed envelope and clears the home's
	// private key.
	Approve(ctx context.Context, id, dbRole string, sealed json.RawMessage, by string, now time.Time) error
	// Reject / Revoke set the state and drop the keys and the envelope.
	Reject(ctx context.Context, id string, now time.Time) error
	Revoke(ctx context.Context, id string, now time.Time) error
	UpdateNode(ctx context.Context, id string, p NodePatch, now time.Time) error
	DeleteNode(ctx context.Context, id string) error

	// Stage switches: nil removes the row (automatic).
	SetStages(ctx context.Context, id string, stages map[string]*bool) error
	Stages(ctx context.Context) (map[string]map[string]bool, error)
	Workers(ctx context.Context) ([]NodeWorker, error)

	// ClearSealed drops envelopes of approved machines whose worker has
	// registered, or that were approved before cutoff.
	ClearSealed(ctx context.Context, cutoff time.Time) error
}
