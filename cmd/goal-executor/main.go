package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/ginoh/goal-executor/internal/control"
	"github.com/ginoh/goal-executor/internal/dockerruntime"
	"github.com/ginoh/goal-executor/internal/goalfile"
	"github.com/ginoh/goal-executor/internal/planning"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if err := run(ctx, os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, err)
		if errors.Is(err, context.Canceled) {
			os.Exit(130)
		}
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, out, errOut io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: goal-executor run --goal FILE | cleanup --environment NAME")
	}
	if args[0] != "run" && args[0] != "cleanup" {
		return fmt.Errorf("unknown command %q", args[0])
	}
	flags := flag.NewFlagSet(args[0], flag.ContinueOnError)
	flags.SetOutput(errOut)
	goalPath := flags.String("goal", "", "YAML goal file (run)")
	environment := flags.String("environment", "", "environment to clean up (cleanup)")
	dbImage := flags.String("db-image", "postgres:17-alpine", "locally available DB image")
	apiImage := flags.String("api-image", "goal-executor-demo:local", "locally built API image")
	timeout := flags.Duration("operation-timeout", 90*time.Second, "timeout for each observation, operation, or cleanup")
	interval := flags.Duration("interval", time.Second, "poll interval while achieved or input is invalid")
	delay := flags.Duration("deploy-delay", 0, "optional delay for demonstrating in-flight goal changes")
	once := flags.Bool("once", false, "exit successfully when the current goal is achieved")
	if err := flags.Parse(args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("unexpected positional arguments")
	}
	if *interval <= 0 {
		return errors.New("interval must be positive")
	}
	runtime, err := dockerruntime.New(dockerruntime.Config{DBImage: *dbImage, APIImage: *apiImage, OperationTimeout: *timeout, DeployDelay: *delay})
	if err != nil {
		return err
	}
	if args[0] == "cleanup" {
		if *environment == "" || *goalPath != "" {
			return errors.New("cleanup requires --environment and does not accept --goal")
		}
		if err := runtime.Cleanup(ctx, *environment); err != nil {
			return err
		}
		fmt.Fprintf(out, "cleaned environment %s\n", *environment)
		return nil
	}
	if *goalPath == "" || *environment != "" {
		return errors.New("run requires --goal and does not accept --environment")
	}
	load := func(context.Context) (planning.Goal, error) {
		g, err := goalfile.Load(*goalPath)
		if err != nil {
			return g, err
		}
		if err := dockerruntime.ValidateEnvironment(g.Environment); err != nil {
			return g, err
		}
		if g.Dataset != dockerruntime.Dataset || (g.APIVersion != "v1" && g.APIVersion != "v2") {
			return g, errors.New("demo supports dataset sample-v1 and API v1/v2")
		}
		return g, nil
	}
	loopCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	encoder := json.NewEncoder(out)
	var achieved bool
	var outputErr error
	err = control.Run(loopCtx, load, runtime, control.Options{Interval: *interval, Report: func(e control.Event) {
		entry := struct {
			Time   string          `json:"time"`
			Event  string          `json:"event"`
			Goal   planning.Goal   `json:"goal"`
			State  planning.State  `json:"state"`
			Plan   planning.Result `json:"plan"`
			Action planning.Action `json:"action"`
			Error  string          `json:"error,omitempty"`
		}{Time: time.Now().UTC().Format(time.RFC3339Nano), Event: e.Kind, Goal: e.Goal, State: e.State, Plan: e.Plan, Action: e.Action}
		if e.Err != nil {
			entry.Error = e.Err.Error()
		}
		if outputErr == nil {
			outputErr = encoder.Encode(entry)
		}
		if outputErr != nil {
			cancel()
			return
		}
		if *once && e.Kind == "planned" && e.Plan.Status == planning.Achieved {
			achieved = true
			cancel()
		}
	}})
	if outputErr != nil {
		return fmt.Errorf("write execution log: %w", outputErr)
	}
	if achieved && errors.Is(err, context.Canceled) && ctx.Err() == nil {
		return nil
	}
	return err
}
