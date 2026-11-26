package privacy

import (
	"encoding/json"
	"fmt"
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
	// AccountData is what a Maktaba Cloud account holds and for how long; Processors, who handles
	// it for us.
	AccountData   []string `json:"account_data"`
	Processors    []string `json:"processors"`
	LawfulBasis   string   `json:"lawful_basis"`
	RetentionDays int      `json:"retention_days"`
	Rights        []string `json:"rights"`
	UpdatedAt     string   `json:"updated_at"`
}

// CurrentPolicy returns the cloud's privacy policy: the relay's, and the accounts'. Static content; the
// UpdatedAt is the policy revision date, not "now" (kept deterministic).
func CurrentPolicy() Policy {
	return Policy{
		Controller: "Hamza Labs",
		Contact:    "privacy@maktaba.tv",
		Purpose: "Operate and secure the Maktaba cloud relay: capacity " +
			"planning, abuse prevention, and aggregate service health; and run the accounts people make on Maktaba Cloud.",
		DataCategories: []string{
			"Aggregate connection counts (no server identity)",
			"Aggregate bandwidth and request volume",
			"Country of request (derived at the edge, IP discarded)",
			"Aggregate push-delivery outcomes",
		},
		NotCollected: []string{
			"IP addresses in analytics",
			"Server identifiers in analytics",
			"User identifiers in analytics",
			"Media titles, filenames, or content",
			"Request URLs or payloads in analytics",
		},
		AccountData: []string{
			"Email address, name, language, password hash, plan and homes: until the account is deleted, 30 days after its owner asks",
			"A home's public IP address, for direct connections, and how much it relayed, every 5 minutes and every month: until the home is removed",
			"Sign-in sessions, with the device's name and IP address: up to 31 days after they were last used",
			"The cloud's request log, with the IP address and a code for the account: until it is overwritten",
			fmt.Sprintf("Security audit records, which may include an IP address or email address, expire after %d days. Abuse signals, which can be tied to an account, home, or IP address, expire after %d days. Deleting an account removes records linked directly to it or its homes. Records linked only to an IP address expire on schedule.", AuditRetentionDays, AbuseRetentionDays),
			"Account email: its content sealed until it is sent; then the address as a keyed hash, with the kind, time and outcome, 35 days; the network that asked for it, as a keyed hash, a day",
			"Backups of the database: 14 days",
		},
		Processors: []string{
			"Resend (account email): sent from Ireland, stored in the United States; the address, the content and the delivery record, 30 days; an address that bounced or complained stays on its suppression list",
			"Hetzner (the cloud's servers): Finland",
			"Apple and Google (push notifications), for an app that asks for them",
			"Cloudflare (the bot check on the web app's sign-in, sign-up and reset pages, when it is on): the visitor's IP address and what their browser shows it",
		},
		LawfulBasis: "Legitimate interest (Art. 6(1)(f)) in operating a reliable, abuse-resistant relay, balanced by collecting only anonymous aggregates; " +
			"for an account and its email, the contract with its owner (Art. 6(1)(b)).",
		RetentionDays: RetentionDays,
		Rights: []string{
			"Access", "Erasure", "Restriction", "Objection", "Portability",
		},
		UpdatedAt: "2026-11-26",
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
		{
			Name:    "Bot check (when it is on)",
			Purpose: "Tell people from bots on the web app's sign-in, sign-up and reset pages.",
			Categories: []string{
				"The visitor's IP address, and what their browser shows the check (Cloudflare Turnstile)",
			},
			Recipients: []string{"Cloudflare (Turnstile): in the visitor's browser, and the IP address with the check's answer from the api"},
			Retention:  "Nothing here: the answer is used once. At Cloudflare: its Turnstile privacy addendum.",
			Safeguards: []string{
				"Off without both its keys; no cookie of ours; never asked of the apps",
			},
		},
		{
			Name:    "Account mail",
			Purpose: "Confirm an account's email, reset its password and send its notices.",
			Categories: []string{
				"Email address and the mail's content (to the recipient)",
				"Kind, language, time and outcome of each mail; the address as a keyed hash",
			},
			Recipients: []string{
				"The account's owner",
				"Resend (delivery): sent from Ireland (eu-west-1), stored in the United States",
			},
			Retention: "Here: the content until the mail is sent or given up, sealed; the rest 35 days. At Resend: the address, content, metadata, " +
				"events and logs 30 days, and backups 7 days more; an address that bounced or complained stays on its suppression list.",
			Safeguards: []string{
				"Transfer to the United States under the EU Standard Contractual Clauses in Resend's DPA, and its EU-U.S. Data Privacy Framework listing",
				"Links in the mails work for an hour (a reset) or a day (a confirmation), and a newer one ends an older one",
				"Content sealed at rest and cleared once sent; no address, link or content in logs",
				"No open or click tracking, no remote images",
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
