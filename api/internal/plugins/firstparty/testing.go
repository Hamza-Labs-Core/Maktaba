package firstparty

import "sync"

// UseBundle replaces the embedded bundle with raw until the answered function restores it.
// Tests and the parity tool's dev API (tools/plugin-api-dev) only: a release has no way to
// call it (no flag, setting or environment variable reaches it).
func UseBundle(raw []byte) func() {
	fpList, fpDigest, fpErr = parseBundle(raw)
	fpOnce = sync.Once{}
	fpOnce.Do(func() {})
	return func() {
		fpOnce = sync.Once{}
		fpList, fpDigest, fpErr = nil, "", nil
	}
}
