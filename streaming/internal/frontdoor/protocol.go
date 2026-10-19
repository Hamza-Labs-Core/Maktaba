package frontdoor

// EdgeProtocol is the front door's protocol revision (Epic 35 design §7.3). The
// release workflow records it in release.json as edge_protocol
// (tools/release/cmd/gen-release-json reads this line); an update restarts the
// running front door only when the new release's value differs. Raise it when
// a change here needs the front door restarted.
const EdgeProtocol = 1
