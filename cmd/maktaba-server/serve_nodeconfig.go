package main

import (
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/config"
)

// nodeConfigSettleLimit is how long serve waits for a write of node.json still under way
// before it starts (nodeConfigSettles): a write takes milliseconds, and a lock a process
// that stopped mid-write left counts as one for a minute only (config.ErrBeingWritten).
const nodeConfigSettleLimit = 75 * time.Second

// nodeConfigSettles runs check (serve's check of the node config) until it says
// anything but that node.json is being written (config.ErrBeingWritten), looking again
// every poll for up to limit, and says once that it waits: serve starts once the write
// ended, rather than as the home beside it or not at all.
func nodeConfigSettles(check func() error, poll, limit time.Duration, log io.Writer) error {
	deadline := time.Now().Add(limit)
	said := false
	for {
		err := check()
		if !errors.Is(err, config.ErrBeingWritten) || !time.Now().Before(deadline) {
			return err
		}
		if !said {
			fmt.Fprintf(log, "[serve] %v: starting once the write ends\n", err)
			said = true
		}
		time.Sleep(poll)
	}
}
