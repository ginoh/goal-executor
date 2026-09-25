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
	"reflect"
	"syscall"
	"time"

	"github.com/ginoh/goal-executor/internal/buildinput"
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
	timeout := flags.Duration("operation-timeout", 90*time.Second, "timeout for each observation or operation")
	interval := flags.Duration("interval", time.Second, "poll interval while achieved or input is invalid")
	once := flags.Bool("once", false, "exit when the current goal is achieved")
	if err := flags.Parse(args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("unexpected positional arguments")
	}
	if *timeout <= 0 || *interval <= 0 {
		return errors.New("timeout and interval must be positive")
	}
	if args[0] == "cleanup" {
		if *environment == "" || *goalPath != "" {
			return errors.New("cleanup requires --environment and does not accept --goal")
		}
		r, err := dockerruntime.New(dockerruntime.Config{Environment: *environment, OperationTimeout: *timeout})
		if err != nil {
			return err
		}
		if err := r.Cleanup(ctx, *environment); err != nil {
			return err
		}
		_, err = fmt.Fprintf(out, "cleaned environment %s\n", *environment)
		return err
	}
	if *goalPath == "" || *environment != "" {
		return errors.New("run requires --goal and does not accept --environment")
	}
	initial, err := goalfile.Load(*goalPath)
	if err != nil {
		return err
	}
	if err := dockerruntime.ValidateEnvironment(initial.Environment); err != nil {
		return err
	}
	snapshot, err := buildinput.Prepare(*goalPath, initial.Application.Build.Context, initial.Application.Build.Dockerfile)
	if err != nil {
		return err
	}
	defer os.RemoveAll(snapshot.Dir)
	r, err := dockerruntime.New(dockerruntime.Config{Environment: initial.Environment, Build: snapshot, Application: initial.Application, Checks: initial.Checks, OperationTimeout: *timeout})
	if err != nil {
		return err
	}
	load := fixedGoalLoader(*goalPath, initial, snapshot.ID)
	loopCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	encoder := json.NewEncoder(out)
	var achieved bool
	var outputErr error
	err = control.Run(loopCtx, load, r, control.Options{Interval: *interval, Report: func(e control.Event) {
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

func fixedGoalLoader(path string, initial goalfile.Document, inputID string) control.LoadGoal {
	return func(context.Context) (planning.Goal, error) {
		d, err := goalfile.Load(path)
		if err != nil {
			return planning.Goal{}, err
		}
		if d.Environment != initial.Environment || !reflect.DeepEqual(d.Application, initial.Application) || !reflect.DeepEqual(d.Checks, initial.Checks) {
			return planning.Goal{}, errors.New("configuration changed; restore it or restart the CLI")
		}
		return planning.Goal{Environment: d.Environment, InputID: inputID, State: d.Goal.State}, nil
	}
}
