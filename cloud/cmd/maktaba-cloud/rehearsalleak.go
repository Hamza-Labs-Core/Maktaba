//go:build rehearsalleak

package main

import (
	"net/http"
	"sync"
)

// TEMPORARY: a relay that holds 64 KB per tunnel connect, to show load-small's heap gate
// catches a per-tunnel leak. Removed before merge.
var (
	leakMu sync.Mutex
	leaked [][]byte
)

func init() {
	evidenceWrap = func(h http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			leakMu.Lock()
			leaked = append(leaked, make([]byte, 64<<10))
			leakMu.Unlock()
			h(w, r)
		}
	}
}
