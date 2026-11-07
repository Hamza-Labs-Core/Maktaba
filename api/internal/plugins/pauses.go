package plugins

import (
	"context"
	"database/sql"

	"github.com/Hamza-Labs-Core/Maktaba/shared/components"
)

// Paused is an installed plugin a server version would pause (story 54.07 §3): its
// manifest's maktaba range excludes that version, so after the update it waits
// incompatible, its queue kept. The update is not blocked.
type Paused struct {
	ID      string `json:"id"`
	Version string `json:"version"`
	// Range is the plugin's maktaba range, which the version is outside.
	Range string `json:"range"`
	// Name is the plugin's own name by language (en, ar).
	Name map[string]string `json:"name"`
	// Enabled: an admin turned it on (an installed, off plugin pauses nothing that runs).
	Enabled bool `json:"enabled"`
}

// PausedBy lists the installed plugins whose maktaba range excludes version, by id.
func PausedBy(ctx context.Context, db *sql.DB, version string) ([]Paused, error) {
	rows, err := db.QueryContext(ctx, `SELECT i.plugin_id, i.version, i.path, i.manifest,
		COALESCE(c.want, '') FROM plugin_installs i
		LEFT JOIN components c ON c.id = $1 || i.plugin_id ORDER BY i.plugin_id`, components.PluginPrefix)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Paused{}
	for rows.Next() {
		var p Paused
		var path, want string
		var raw []byte
		if err := rows.Scan(&p.ID, &p.Version, &path, &raw, &want); err != nil {
			return nil, err
		}
		m, err := parseManifest(raw)
		if err != nil || m.Maktaba == "" || components.VersionInRange(version, m.Maktaba) {
			continue
		}
		p.Range, p.Enabled = m.Maktaba, want == components.WantEnabled
		p.Name = namesOf(path, m).Name
		out = append(out, p)
	}
	return out, rows.Err()
}
