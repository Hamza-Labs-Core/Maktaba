package cluster

import (
	"bytes"
	"encoding/json"
	"net"
	"net/url"
	"strconv"
)

// PayloadVersion is the sealed payload's "v".
const PayloadVersion = 1

// PayloadHome identifies the home inside the sealed payload.
// ClusterPub (phase 2) is the home's long-term cluster key, which the
// machine's agent checks in the channel handshake (design-agent.md §4.2);
// empty only in the phase-1 vectors.
type PayloadHome struct {
	APIURL     string `json:"api_url"`
	ClusterPub string `json:"cluster_pub,omitempty"`
	InstanceID string `json:"instance_id"`
	Name       string `json:"name"`
}

// Payload is what the home seals to an approved machine (design §3.2).
// Fields are declared in key order so the encoding matches the
// sorted-keys JSON of shared/testdata/cluster/vectors.json byte for byte.
type Payload struct {
	// DatabaseCA is the home's database CA (PEM), which the machine verifies it with;
	// DatabaseTLS is "off" when the home's database has no TLS. Neither for a database
	// that is not the bundled one (its own TLS, which the home cannot vouch for).
	DatabaseCA  string      `json:"database_ca,omitempty"`
	DatabaseTLS string      `json:"database_tls,omitempty"`
	DatabaseURL string      `json:"database_url"`
	Home        PayloadHome `json:"home"`
	NodeID      string      `json:"node_id"`
	V           int         `json:"v"`
}

// DatabaseURL builds the machine's connection string: its own role and
// password, the host the machine used to reach the API, the given port
// and the home's database name, sslmode=prefer.
func DatabaseURL(role, password, host string, port int, dbName string) string {
	u := url.URL{
		Scheme:   "postgres",
		User:     url.UserPassword(role, password),
		Host:     net.JoinHostPort(host, strconv.Itoa(port)),
		Path:     "/" + dbName,
		RawQuery: "sslmode=prefer",
	}
	return u.String()
}

// RoleName is the machine's database role: maktaba_node_<the id's 32 hex
// digits>. The whole id, never a prefix: the machine chooses its id, and a
// shared prefix would hand a second machine the first one's role.
func RoleName(nodeID string) string {
	hex := make([]byte, 0, 32)
	for i := 0; i < len(nodeID) && len(hex) < 32; i++ {
		c := nodeID[i]
		switch {
		case c >= '0' && c <= '9', c >= 'a' && c <= 'f':
			hex = append(hex, c)
		case c >= 'A' && c <= 'F':
			hex = append(hex, c+('a'-'A'))
		}
	}
	return "maktaba_node_" + string(hex)
}

// MarshalPayload encodes p as compact JSON without HTML escaping (the
// Python side's json.dumps(sort_keys=True, separators=(",", ":")) form).
func MarshalPayload(p Payload) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(p); err != nil {
		return nil, err
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}
