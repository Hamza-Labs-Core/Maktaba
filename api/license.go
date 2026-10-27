package main

import (
	"context"
	"database/sql"
	"log/slog"
	"net/url"
	"os"
	"strings"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/cloudlink"
	subh "github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/subscriptions"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/router"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/subscriptions"
)

// defaultLicenseServer is Maktaba Cloud's API, the license server a home
// asks for the revocation list when it is not claimed in a cloud of its
// own (MAKTABA_LICENSE_SERVER overrides; "off" asks no one).
const defaultLicenseServer = "https://api.maktaba.app"

// licenseRefresher builds the Epic 16 story 16.4 refresher: the signed
// revocation list daily from the license server, and, for a home the
// relay agent claimed in Maktaba Cloud, the license its owner's plan
// grants (through the agent, which holds the server secret). It also
// returns the cloud link Settings shows and the cloud's billing page.
// A build without the license key gets no refresher (nothing to verify
// with).
func licenseRefresher(store *subscriptions.Store, v *subscriptions.Verifier, db *sql.DB, logger *slog.Logger) (*subscriptions.Refresher, func() subh.CloudLink, string) {
	ids := &cloudlink.IdentitySource{Path: os.Getenv("MAKTABA_CLOUD_IDENTITY")}
	cloud := func() subh.CloudLink {
		id, _, err := ids.Load()
		if err != nil {
			return subh.CloudLink{}
		}
		return subh.CloudLink{Claimed: true, ServerID: id.ServerID, Slug: id.Slug, CloudAPI: id.CloudAPI}
	}
	serverURL := cloudAPIURL(cloud)
	manage := os.Getenv("MAKTABA_CLOUD_WEB_URL")
	if manage == "" {
		manage = cloudWebFromAPI(serverURL())
	}
	if manage != "" {
		manage = strings.TrimRight(manage, "/") + "/billing"
	}
	if v == nil {
		return nil, cloud, manage
	}
	r := &subscriptions.Refresher{
		Store:     store,
		Verifier:  v,
		ServerURL: serverURL,
		AgentAddr: os.Getenv("MAKTABA_CLOUDLINK_ADMIN"),
		ServerID:  func() string { return cloud().ServerID },
		Log:       logger,
	}
	if db != nil {
		audit := router.LicenseAudit(db)
		r.Event = func(ctx context.Context, event string, detail map[string]any) { audit(ctx, "", event, detail) }
	}
	return r, cloud, manage
}

// cloudAPIURL is Maktaba Cloud's API for this home: the cloud it is claimed in,
// else MAKTABA_LICENSE_SERVER, else defaultLicenseServer; "" when that says
// "off".
func cloudAPIURL(cloud func() subh.CloudLink) func() string {
	server := strings.TrimSpace(os.Getenv("MAKTABA_LICENSE_SERVER"))
	if server == "" {
		server = defaultLicenseServer
	}
	return func() string {
		if c := cloud(); c.CloudAPI != "" {
			return c.CloudAPI
		}
		if server == "off" {
			return ""
		}
		return server
	}
}

// telemetryURL is where usage data goes (Epic 16 story 16.5): Maktaba Cloud's
// API as the license server finds it, unless MAKTABA_TELEMETRY_URL names
// another ("off": nowhere; what waits ages out).
func telemetryURL(cloud func() subh.CloudLink) func() string {
	switch v := strings.TrimSpace(os.Getenv("MAKTABA_TELEMETRY_URL")); v {
	case "":
		return cloudAPIURL(cloud)
	case "off":
		return func() string { return "" }
	default:
		return func() string { return v }
	}
}

// cloudWebFromAPI guesses the cloud web app from its API origin
// (https://api.example → https://example); "" when it cannot.
func cloudWebFromAPI(api string) string {
	u, err := url.Parse(api)
	if err != nil || u.Host == "" || !strings.HasPrefix(u.Host, "api.") {
		return ""
	}
	u.Host = strings.TrimPrefix(u.Host, "api.")
	u.Path = ""
	return u.String()
}
