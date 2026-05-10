# Observability deploy assets

Owned by Epic 21 (stories 21.2, 21.4, 21.5).

* `grafana/` — board JSON. Stable filenames are an API: the Go manifest
  in `shared/metrics/go/dashboards.go` references them by name.
* `prometheus/` — alert rules. The Go manifest in
  `shared/metrics/go/dashboards.go` mirrors the rule names so the API
  can return runbook links keyed by alert name.

## Wiring

The dev compose stack (`deploy/dev/docker-compose.yml`) mounts
`deploy/observability/grafana/` as `/etc/grafana/provisioning/dashboards/`
and `deploy/observability/prometheus/` as `/etc/prometheus/rules/`.

## Adding a dashboard

1. Author the board in Grafana, click "Share → Export → Save to file".
2. Drop the JSON in `grafana/`.
3. Add a row to `DashboardManifest` in `shared/metrics/go/dashboards.go`.
4. The CI lint job verifies every manifest row has a matching file.
