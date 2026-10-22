package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/adopt"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/config"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/supervisor"
)

// finishMove ends a move of an install into the app (Epic 38 story 38.13) once
// every process ran healthy for confirmAfter: the move is verified (when setup
// did not) and the old install's task removed (its XML stays for adopt --undo).
func finishMove(ctx context.Context, sup *supervisor.Supervisor, cfg config.Config) {
	tgt := adoptTarget(cfg)
	rec, ok, err := adopt.ReadRecord(tgt.StateDir)
	if err != nil || !ok || (rec.Stage != adopt.StageMoved && rec.Stage != adopt.StageVerified) {
		return
	}
	after := confirmAfter()
	if after <= 0 {
		return
	}
	t := time.NewTicker(10 * time.Second)
	defer t.Stop()
	for !healthyFor(sup.Snapshot(), after) {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
	if err := adopt.Verify(ctx, adoptSys(), tgt, cfg, time.Minute); err != nil {
		fmt.Fprintf(os.Stderr, "[serve] the move of %s into the app is not verified yet: %v\n", rec.Source.ID, err)
		return
	}
	if _, err := adopt.Finish(adoptSys(), tgt.StateDir); err != nil {
		fmt.Fprintf(os.Stderr, "[serve] finishing the move of %s: %v\n", rec.Source.ID, err)
		return
	}
	fmt.Fprintf(os.Stderr, "[serve] the move of %s into the app is finished: its old task is removed (adopt --undo brings it back)\n", rec.Source.ID)
}
