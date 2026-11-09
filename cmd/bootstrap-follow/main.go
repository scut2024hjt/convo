package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/scut2024hjt/convo/dao/mysql"
	"github.com/scut2024hjt/convo/settings"
)

// Explicit operator tool: creates/continues historic follow backfill tasks.
// Does not invent publishing events or modify existing votes.
func main() {
	batch := flag.Int("batch", 50, "maximum follow edges per durable bootstrap batch (1..200)")
	confirm := flag.Bool("confirm", false, "acknowledge the selected DB and T02 migration")
	flag.Parse()
	if !*confirm || *batch < 1 || *batch > 200 {
		fmt.Fprintln(os.Stderr, "provide --confirm and --batch in 1..200")
		os.Exit(2)
	}
	if err := settings.Init(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err := mysql.Init(settings.Conf.MySQLConfig); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer mysql.Close()
	total := 0
	for {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		n, done, err := mysql.BootstrapFollowTasks(ctx, *batch)
		cancel()
		if err != nil {
			fmt.Fprintf(os.Stderr, "bootstrap paused with durable checkpoint: %v\n", err)
			os.Exit(1)
		}
		total += n
		fmt.Printf("batch_candidates=%d total_candidates=%d completed=%t\n", n, total, done)
		if done {
			return
		}
	}
}
