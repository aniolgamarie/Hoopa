//go:build linux && amd64

// controlprobe is an isolated experiment, not the product control API.
package main

import (
	"context"
	"flag"
	"fmt"
	events "hoopa/experiments/mihomo-events"
	"io"
	"os"
	"os/signal"
	"syscall"
)

func main() { os.Exit(run(os.Args[1:])) }
func run(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "controlprobe: init, serve or launch required")
		return 2
	}
	f := flag.NewFlagSet(args[0], flag.ContinueOnError)
	f.SetOutput(io.Discard)
	switch args[0] {
	case "launch":
		intent := f.String("intent", "", "absolute durable launch intent")
		assets := f.String("assets", "", "absolute private pinned assets record")
		if f.Parse(args[1:]) != nil || f.NArg() != 0 || *intent == "" || *assets == "" {
			return 2
		}
		if events.RunLinuxControlLauncher(*intent, *assets) != nil {
			fmt.Fprintln(os.Stderr, "controlprobe: protected launch refused")
			return 1
		}
		return 0
	case "init":
		instance := f.String("instance", "", "absolute private instance directory")
		if f.Parse(args[1:]) != nil || f.NArg() != 0 || *instance == "" {
			return 2
		}
		if events.InitControlInstance(*instance) != nil {
			fmt.Fprintln(os.Stderr, "controlprobe: initialization refused")
			return 1
		}
		fmt.Fprintln(os.Stdout, "controlprobe: initialized")
		return 0
	case "serve":
		instance := f.String("instance", "", "absolute private instance directory")
		assets := f.String("assets", "", "absolute private pinned assets record")
		crash := f.String("crash-at", "", "explicit owned-guest scenario boundary")
		if f.Parse(args[1:]) != nil || f.NArg() != 0 || *instance == "" || *assets == "" {
			return 2
		}
		backend, e := events.NewLinuxControlBackend(*assets)
		if e != nil {
			fmt.Fprintln(os.Stderr, "controlprobe: assets refused")
			return 1
		}
		ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
		defer cancel()
		e = events.ServeControlInstance(ctx, *instance, backend, events.ControlServeOptions{CrashAt: *crash, Ready: func() { fmt.Fprintln(os.Stdout, "controlprobe: ready") }})
		if e != nil {
			fmt.Fprintln(os.Stderr, "controlprobe: service refused")
			return 1
		}
		return 0
	default:
		fmt.Fprintln(os.Stderr, "controlprobe: unsupported subcommand")
		return 2
	}
}
