//go:build linux && amd64 && p1integration

// p1probe is an explicitly built integration driver, never a shipped command.
// Frontends still use the normal hoopa executable. This driver runs the actual
// service or public BasicApplier with a fixed finite test source and inherited
// supervisor channel. No ambient environment variable enables the channel.
package main

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"os/signal"
	"strconv"
	"syscall"

	"hoopa/internal/cli"
	"hoopa/internal/config"
	"hoopa/internal/control"
	"hoopa/internal/p1testhook"
	"hoopa/internal/service"
	"hoopa/internal/testsupport/p1fixture"
	"hoopa/internal/testsupport/p1session"
	"hoopa/internal/testsupport/p1trace"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if len(os.Args) == 2 && os.Args[1] == "__hoopa_core_launcher" {
		os.Exit(cli.Execute(ctx, os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
	}
	producer, command, arguments, err := traceBootstrap(ctx, os.Args[1:])
	if err != nil {
		os.Exit(2)
	}
	defer producer.Close()
	err = p1testhook.Install(func(e p1testhook.Event) bool {
		data, err := json.Marshal(struct {
			Injectable bool            `json:"injectable"`
			Detail     json.RawMessage `json:"detail"`
		}{e.Injectable, e.Data})
		if err != nil {
			os.Exit(97)
		}
		decision, err := producer.Emit(context.Background(), p1trace.Event{Component: e.Component, Point: e.Point, OperationID: e.OperationID, RunID: e.RunID, RequestID: e.RequestID, Data: data})
		if err != nil {
			_, _ = io.WriteString(os.Stderr, "P1_TRACE_LOST\n")
			os.Exit(97)
		}
		if decision == p1trace.InjectError && !e.Injectable {
			os.Exit(98)
		}
		return decision == p1trace.InjectError
	})
	if err != nil {
		os.Exit(2)
	}
	switch command {
	case "service":
		args := []string{"service", "run"}
		if len(arguments) == 1 && arguments[0] == "--restart-once" {
			args = append(args, "--restart-once")
		} else if len(arguments) != 0 {
			os.Exit(2)
		}
		os.Exit(cli.Execute(ctx, args, os.Stdin, os.Stdout, os.Stderr))
	case "seed-capacity":
		if len(arguments) != 0 {
			os.Exit(2)
		}
		os.Exit(seedCapacity(ctx))
	case "apply":
		if len(arguments) != 4 {
			os.Exit(2)
		}
		os.Exit(apply(ctx, arguments))
	default:
		os.Exit(2)
	}
}

func apply(ctx context.Context, args []string) int {
	raw, err := io.ReadAll(io.LimitReader(os.Stdin, control.MaxRequestBytes+1))
	if err != nil || len(raw) > control.MaxRequestBytes {
		return 2
	}
	env := map[string]string{}
	for _, key := range []string{"XDG_CONFIG_HOME", "XDG_STATE_HOME", "XDG_CACHE_HOME", "XDG_RUNTIME_DIR"} {
		env[key] = os.Getenv(key)
	}
	paths, err := config.ResolvePaths(os.Getenv("HOME"), env)
	var result []byte
	if err == nil {
		var c *control.Coordinator
		var a *control.BasicApplier
		c, a, err = p1fixture.OpenApplier(paths, args[0], args[1], map[string]string{"app-first": args[2], "domain-first": args[3]}, []string{"__hoopa_core_launcher"})
		if err == nil {
			defer c.Close()
			var stored control.StoredOperation
			stored, _, err = a.Apply(ctx, raw)
			if len(stored.Record.Result) != 0 {
				result = stored.Record.Result
			}
		}
	}
	if len(result) == 0 {
		result = service.Failure(raw, err)
	}
	if _, err = os.Stdout.Write(append(append([]byte(nil), result...), '\n')); err != nil {
		return 4
	}
	return service.ExitCode(result)
}

// This is a tagged fixture-driver summary, never a product API response.
func seedCapacity(ctx context.Context) int {
	env := map[string]string{}
	for _, key := range []string{"XDG_CONFIG_HOME", "XDG_STATE_HOME", "XDG_CACHE_HOME", "XDG_RUNTIME_DIR"} {
		env[key] = os.Getenv(key)
	}
	paths, err := config.ResolvePaths(os.Getenv("HOME"), env)
	if err != nil {
		_, _ = io.WriteString(os.Stderr, "P1_CAPACITY_SEED_FAILED\n")
		return 4
	}
	summary, err := p1fixture.SeedCapacity(ctx, paths)
	if err != nil {
		_, _ = io.WriteString(os.Stderr, "P1_CAPACITY_SEED_FAILED\n")
		return 4
	}
	raw, err := json.Marshal(summary)
	if err != nil || len(raw)+1 > 1<<20 {
		_, _ = io.WriteString(os.Stderr, "P1_CAPACITY_SEED_FAILED\n")
		return 4
	}
	raw = append(raw, '\n')
	n, err := os.Stdout.Write(raw)
	if err != nil || n != len(raw) {
		return 4
	}
	return 0
}

// Both modes are explicitly tagged test-driver entry points. The old inherited
// FD protocol retains its actual-parent check. Remote sessions use only the
// authenticated broker protocol and cannot apply or arm restart at bootstrap.
func traceBootstrap(ctx context.Context, args []string) (*p1trace.Producer, string, []string, error) {
	if len(args) >= 4 && args[0] == "trace" {
		parent, err := strconv.Atoi(args[2])
		if err != nil || parent != os.Getppid() {
			return nil, "", nil, p1trace.ErrTransport
		}
		file := os.NewFile(3, "supervisor")
		producer, err := p1trace.NewProducer(file, parent, os.Getuid(), args[1])
		file.Close()
		return producer, args[3], args[4:], err
	}
	if len(args) == 5 && args[0] == "session-trace" && args[4] == "service" {
		supervisor, err := strconv.Atoi(args[3])
		if err != nil || supervisor <= 0 {
			return nil, "", nil, p1session.ErrBootstrap
		}
		producer, err := p1session.Connect(ctx, args[1], args[2], supervisor, os.Getuid())
		return producer, "service", nil, err
	}
	return nil, "", nil, p1trace.ErrTransport
}
