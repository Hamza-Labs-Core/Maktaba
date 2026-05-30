//go:build !race

package cloudlink

// raceEnabled is false in normal (non -race) builds; the cross-component
// happy-path body test runs and asserts a full round-trip through the
// real cloud relay.Tunnel.
const raceEnabled = false
