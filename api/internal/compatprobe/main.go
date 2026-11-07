// compat-prev's schema probe (tools/compat-prev/run.sh): copied into a checkout's
// api/internal/compatprobe and run there, so it is that release's own node schema guard
// (api/internal/cluster/schemaguard) that reads a home's database and says whether a node
// of that release pauses against it.
package main

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"time"

	_ "github.com/lib/pq"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/schemaguard"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: compatprobe <home database URL>")
		os.Exit(2)
	}
	db, err := sql.Open("postgres", os.Args[1])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	h := schemaguard.Read(ctx, db)
	fmt.Printf("rev=%d\n", schemaguard.SchemaRev)
	fmt.Printf("applied=%d\n", h.Applied)
	fmt.Printf("min_node=%d\n", h.MinNode)
	fmt.Printf("pause=%s\n", schemaguard.PauseReason(h, schemaguard.SchemaRev))
}
