package delegation

import "time"

// The node ↔ home wire shapes of story 35.9, shared by the home's handlers
// (handlers/clusterauth) and the node's link (cluster/homelink).

// Principal is the user a node acts for, as the home resolved it.
type Principal struct {
	UserID             string   `json:"user_id"`
	Username           string   `json:"username"`
	IsAdmin            bool     `json:"is_admin"`
	Libraries          []string `json:"libraries"`
	AccessAllLibraries bool     `json:"access_all_libraries"`
	// MustChangePassword: the session's password was reset by an admin; the
	// node's auth gate allows only the change-password form (an older home
	// leaves it out, which reads as false).
	MustChangePassword bool `json:"must_change_password,omitempty"`
}

// IntrospectRequest names one credential (POST /api/cluster/delegate/introspect).
type IntrospectRequest struct {
	Session string `json:"session,omitempty"`
	PAT     string `json:"pat,omitempty"`
}

// IntrospectAnswer is the home's answer (design §10.3).
type IntrospectAnswer struct {
	Principal    Principal `json:"principal"`
	Source       string    `json:"source"` // cookie | pat
	CSRFSHA256   string    `json:"csrf_sha256,omitempty"`
	ExpiresAt    time.Time `json:"expires_at"`
	OriginNodeID *string   `json:"origin_node_id"`
}

// ExchangeRequest is the node's half of the cluster sign-in (POST /api/cluster/sso/exchange).
type ExchangeRequest struct {
	Code     string `json:"code"`
	Verifier string `json:"verifier"`
}

// ExchangeAnswer is the child session the node sets as its own host's cookie.
type ExchangeAnswer struct {
	SessionID  string    `json:"session_id"`
	CSRFToken  string    `json:"csrf_token"`
	ExpiresAt  time.Time `json:"expires_at"`
	ReturnPath string    `json:"return_path"`
	Principal  Principal `json:"principal"`
}

// Revoked is the auth.revoked frame's body as the node receives it: sessions by the
// SHA-256 (hex) of their id — a node keys its cache by that and never needs the id —
// or every credential of a user.
type Revoked struct {
	SessionSHA256 []string `json:"session_sha256,omitempty"`
	UserID        string   `json:"user_id,omitempty"`
	Reason        string   `json:"reason,omitempty"`
}

// SignOutRequest is a sign-out made on a node while the home did not answer
// (POST /api/cluster/delegate/signout), sent once the home answers again: sessions by
// the SHA-256 (hex) of their id, and/or every session of a user made before a moment
// ("sign out everywhere").
type SignOutRequest struct {
	SessionSHA256 []string   `json:"session_sha256,omitempty"`
	UserID        string     `json:"user_id,omitempty"`
	Before        *time.Time `json:"before,omitempty"`
}

// SignOutAnswer is how many sessions the home ended.
type SignOutAnswer struct {
	Revoked int `json:"revoked"`
}
