package privacy

import (
	"encoding/json"
	"net/http"
)

// Policy is the machine-readable privacy summary served at GET /privacy.
// It documents what the relay processes and the user's rights — the
// transparency obligation (GDPR Art. 13/14) in a form clients can render.
type Policy struct {
	Controller     string   `json:"controller"`
	Contact        string   `json:"contact"`
	Purpose        string   `json:"purpose"`
	DataCategories []string `json:"data_categories"`
	NotCollected   []string `json:"not_collected"`
	LawfulBasis    string   `json:"lawful_basis"`
	RetentionDays  int      `json:"retention_days"`
	Rights         []string `json:"rights"`
	UpdatedAt      string   `json:"updated_at"`
}

// CurrentPolicy returns the relay's privacy policy. Static content; the
// UpdatedAt is the policy revision date, not "now" (kept deterministic).
func CurrentPolicy() Policy {
	return Policy{
		Controller: "Hamza Labs",
		Contact:    "privacy@maktaba.app",
		Purpose: "Operate and secure the Maktaba cloud relay: capacity " +
			"planning, abuse prevention, and aggregate service health.",
		DataCategories: []string{
			"Aggregate connection counts (no server identity)",
			"Aggregate bandwidth and request volume",
			"Country of request (derived at the edge, IP discarded)",
			"Aggregate push-delivery outcomes",
		},
		NotCollected: []string{
			"IP addresses",
			"Server identifiers in analytics",
			"User identifiers in analytics",
			"Media titles, filenames, or content",
			"Request URLs or payloads",
		},
		LawfulBasis:   "Legitimate interest (Art. 6(1)(f)) in operating a reliable, abuse-resistant relay, balanced by collecting only anonymous aggregates.",
		RetentionDays: RetentionDays,
		Rights: []string{
			"Access", "Erasure", "Restriction", "Objection", "Portability",
		},
		UpdatedAt: "2026-06-19",
	}
}

// PolicyHandler serves GET /privacy (public, no auth). Mounted on the
// relay role before the proxy catch-all.
func PolicyHandler(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(CurrentPolicy())
}

// ProcessingActivity is one GDPR Article 30 record-of-processing entry.
type ProcessingActivity struct {
	Name       string   `json:"name"`
	Purpose    string   `json:"purpose"`
	Categories []string `json:"data_categories"`
	Recipients []string `json:"recipients"`
	Retention  string   `json:"retention"`
	Safeguards []string `json:"safeguards"`
}

// ProcessingRecords returns the Article 30 record of processing
// activities for the relay analytics. Served to operators.
func ProcessingRecords() []ProcessingActivity {
	return []ProcessingActivity{
		{
			Name:    "Relay service-health metrics",
			Purpose: "Capacity planning and reliability monitoring of the cloud relay.",
			Categories: []string{
				"Aggregate connection/bandwidth/request counts",
				"Country of request (edge-derived, no IP)",
			},
			Recipients: []string{"Hamza Labs operations team"},
			Retention:  "Raw aggregates 24h; hourly aggregates 90 days; then auto-purged.",
			Safeguards: []string{
				"Aggregate-only schema (no user/server id, no IP)",
				"Country derived at edge; IP never persisted",
				"Access restricted to operator email domain",
				"TLS in transit; encrypted at rest",
			},
		},
		{
			Name:    "Push delivery accounting",
			Purpose: "Diagnose and improve push-notification delivery.",
			Categories: []string{
				"User id, device platform, delivery status (existing push log)",
			},
			Recipients: []string{"Hamza Labs operations team", "APNs", "FCM"},
			Retention:  "Per push-log policy; deleted on account deletion.",
			Safeguards: []string{
				"Deletion-on-account-delete via DataSubjectService",
				"Access restricted to operator email domain",
			},
		},
	}
}

// ProcessingRecordsHandler serves the Article 30 records (operator-gated
// by the caller's mount).
func ProcessingRecordsHandler(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"activities": ProcessingRecords()})
}
