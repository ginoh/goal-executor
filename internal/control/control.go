// Package control connects goal loading, observation, planning, and execution.
package control

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/ginoh/goal-executor/internal/planning"
)

type LoadGoal func(context.Context) (planning.Goal, error)

// Runtime must observe actual resources and validate test evidence. Execute
// returns after an operation completes; successful execution is not observation.
type Runtime interface {
	Observe(context.Context, string) (planning.State, error)
	Execute(context.Context, string, planning.Action) error
}

type Event struct {
	Kind   string
	Goal   planning.Goal
	State  planning.State
	Plan   planning.Result
	Action planning.Action
	Err    error
}

type Options struct {
	// Interval controls waiting after achievement or invalid goal input.
	// Zero defaults to one second; negative durations are invalid.
	Interval time.Duration
	Planner  planning.Options
	// Report is called synchronously. It must not modify the event's Plan.Actions.
	Report func(Event)
}

// Run executes at most one action per observation, then reloads the goal.
// Achievement waits for changes; invalid input pauses operations until fixed.
// Runtime/planning failures stop the loop. Cancellation is propagated to Runtime.
func Run(ctx context.Context, load LoadGoal, runtime Runtime, options Options) error {
	if load == nil || runtime == nil {
		return errors.New("goal loader and runtime are required")
	}
	if options.Interval < 0 || options.Planner.MaxStates < 0 {
		return errors.New("interval and max states must not be negative")
	}
	interval := options.Interval
	if interval == 0 {
		interval = time.Second
	}
	report := options.Report
	if report == nil {
		report = func(Event) {}
	}
	var environment string
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		goal, err := load(ctx)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err == nil {
			// Validate before any observation, including custom non-file loaders.
			err = goal.Validate()
		}
		if err == nil && environment != "" && environment != goal.Environment {
			err = errors.New("environment cannot change during a control session")
		}
		if err != nil {
			report(Event{Kind: "goal-invalid", Err: err})
			if err := wait(ctx, interval); err != nil {
				return err
			}
			continue
		}
		if environment == "" {
			environment = goal.Environment
		}
		state, err := runtime.Observe(ctx, environment)
		if err != nil {
			return fmt.Errorf("observe environment: %w", err)
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		report(Event{Kind: "observed", Goal: goal, State: state})
		plan, err := planning.Plan(goal, state, options.Planner)
		if err != nil {
			return fmt.Errorf("plan: %w", err)
		}
		report(Event{Kind: "planned", Goal: goal, State: state, Plan: plan})
		switch plan.Status {
		case planning.Achieved:
			if err := wait(ctx, interval); err != nil {
				return err
			}
		case planning.Planned:
			if err := ctx.Err(); err != nil {
				return err
			}
			action := plan.Actions[0]
			report(Event{Kind: "executing", Goal: goal, Action: action})
			if err := runtime.Execute(ctx, environment, action); err != nil {
				return fmt.Errorf("execute %s: %w", action.Kind, err)
			}
			report(Event{Kind: "executed", Goal: goal, Action: action})
		case planning.Unreachable, planning.LimitReached:
			return fmt.Errorf("planning stopped (%s): %s", plan.Status, plan.Reason)
		default:
			return fmt.Errorf("unknown plan status %q", plan.Status)
		}
	}
}

func wait(ctx context.Context, interval time.Duration) error {
	timer := time.NewTimer(interval)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
