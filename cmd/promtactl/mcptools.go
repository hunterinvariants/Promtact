package main

import (
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/hunterinvariants/promtact/internal/demotools"
)

// promtactl mcp-tools — the demonstration tool server, standalone.
//
// The implementation lives in internal/demotools because `promtact --demo-tools`
// runs the same thing in-process. Two copies of a demonstration drift, and the
// one that drifts is always the one being shown.

func mcpToolsCommand(args []string) error {
	fs := flag.NewFlagSet("mcp-tools", flag.ContinueOnError)
	port := fs.Int("port", 9200, "loopback port to listen on")
	dir := fs.String("dir", "./promtact-demo", "directory of documents the agent may read")
	seed := fs.Bool("seed", false, "write the demonstration documents into --dir first")
	if err := fs.Parse(args); err != nil {
		return err
	}

	tools, err := demotools.New(strings.TrimSpace(*dir))
	if err != nil {
		return err
	}
	if *seed {
		if err := tools.Seed(); err != nil {
			return err
		}
		fmt.Printf("Wrote the demonstration documents to %s\n", tools.Root())
		fmt.Println("vendor-status.md reads as an ordinary status note. Open it and see:")
		fmt.Println("the instruction it carries has no visible form at all.")
	}

	url, stop, err := tools.Listen(*port)
	if err != nil {
		return err
	}
	defer stop()

	fmt.Printf("\npromtactl mcp-tools on %s\n", url)
	fmt.Printf("Documents  %s\n", tools.Root())
	fmt.Printf("Outbox     %s\n", tools.Outbox())
	fmt.Printf("Tools      %s\n", strings.Join(demotools.Tools, ", "))
	fmt.Println("\nCtrl-C to stop.")

	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	<-signals
	return nil
}
