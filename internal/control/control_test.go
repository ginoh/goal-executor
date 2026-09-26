package control

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/ginoh/goal-executor/internal/planning"
)

type fakeRuntime struct {
	state        planning.State
	actions      []planning.ActionKind
	observations int
	noEffect     bool
	observeErr   error
	executeErr   error
	after        func(planning.ActionKind)
}

func (f *fakeRuntime) Observe(context.Context, string) (planning.State, error) {
	f.observations++
	return f.state, f.observeErr
}
func (f *fakeRuntime) Execute(_ context.Context, _ string, a planning.Action) error {
	f.actions = append(f.actions, a.Kind)
	if f.executeErr != nil {
		return f.executeErr
	}
	if !f.noEffect {
		switch a.Kind {
		case planning.BuildImage:
			f.state.ImageExists = true
		case planning.DeployApp:
			f.state.AppExists, f.state.AppCurrent, f.state.AppReady, f.state.TestValid = true, true, true, false
		case planning.VerifyApp:
			f.state.TestValid = true
		}
	}
	if f.after != nil {
		f.after(a.Kind)
	}
	return nil
}
func goal() planning.Goal { return planning.Goal{Environment: "demo", InputID: "abc", State: "ready"} }

func TestGoalChangeReplansAfterOperation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	current := goal()
	f := &fakeRuntime{after: func(kind planning.ActionKind) {
		if kind == planning.DeployApp {
			current.State = "verified"
		}
	}}
	err := Run(ctx, func(context.Context) (planning.Goal, error) { return current, nil }, f, Options{Report: func(e Event) {
		if e.Kind == "planned" && e.Plan.Status == planning.Achieved {
			cancel()
		}
	}})
	if !errors.Is(err, context.Canceled) || !reflect.DeepEqual(f.actions, []planning.ActionKind{planning.BuildImage, planning.DeployApp, planning.VerifyApp}) || f.observations != 4 {
		t.Fatalf("err=%v actions=%v observations=%d", err, f.actions, f.observations)
	}
}

func TestInvalidGoalPausesAndRecovers(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	current := goal()
	current.State = "bad"
	f := &fakeRuntime{}
	invalid := 0
	err := Run(ctx, func(context.Context) (planning.Goal, error) { return current, nil }, f, Options{Interval: time.Millisecond, Report: func(e Event) {
		if e.Kind == "goal-invalid" {
			invalid++
			current.State = "ready"
		}
		if e.Kind == "planned" && e.Plan.Status == planning.Achieved {
			cancel()
		}
	}})
	if !errors.Is(err, context.Canceled) || invalid != 1 || !reflect.DeepEqual(f.actions, []planning.ActionKind{planning.BuildImage, planning.DeployApp}) {
		t.Fatalf("err=%v invalid=%d actions=%v", err, invalid, f.actions)
	}
}

func TestExecutionSuccessNeedsObservation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	f := &fakeRuntime{noEffect: true, after: func(planning.ActionKind) {}}
	f.after = func(planning.ActionKind) {
		if len(f.actions) == 2 {
			cancel()
		}
	}
	err := Run(ctx, func(context.Context) (planning.Goal, error) { return goal(), nil }, f, Options{})
	if !errors.Is(err, context.Canceled) || !reflect.DeepEqual(f.actions, []planning.ActionKind{planning.BuildImage, planning.BuildImage}) || f.observations != 2 {
		t.Fatalf("err=%v actions=%v observations=%d", err, f.actions, f.observations)
	}
}

func TestStopsOnRuntimeAndPlanningFailures(t *testing.T) {
	failure := errors.New("runtime failure")
	for _, tt := range []struct {
		name    string
		runtime fakeRuntime
		options Options
		want    string
		actions int
	}{
		{"observe", fakeRuntime{observeErr: failure}, Options{}, "observe environment", 0},
		{"execute", fakeRuntime{executeErr: failure}, Options{}, "execute build-image", 1},
		{"limit", fakeRuntime{}, Options{Planner: planning.Options{MaxStates: 1}}, "limit-reached", 0},
		{"invalid observation", fakeRuntime{state: planning.State{AppReady: true}}, Options{}, "plan:", 0},
	} {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			err := Run(ctx, func(context.Context) (planning.Goal, error) { return goal(), nil }, &tt.runtime, tt.options)
			if err == nil || !strings.Contains(err.Error(), tt.want) || len(tt.runtime.actions) != tt.actions || tt.runtime.observations != 1 {
				t.Fatalf("err=%v observations=%d actions=%v", err, tt.runtime.observations, tt.runtime.actions)
			}
			if (tt.runtime.observeErr != nil || tt.runtime.executeErr != nil) && !errors.Is(err, failure) {
				t.Fatal("lost underlying error")
			}
		})
	}
}

func TestAchievementWaitsAndReadsNewGoal(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	current := goal()
	f := &fakeRuntime{state: planning.State{ImageExists: true, AppExists: true, AppCurrent: true, AppReady: true}}
	achieved := 0
	err := Run(ctx, func(context.Context) (planning.Goal, error) { return current, nil }, f, Options{
		Interval: time.Millisecond,
		Report: func(e Event) {
			if e.Kind == "planned" && e.Plan.Status == planning.Achieved {
				achieved++
				if achieved == 1 {
					current.State = "verified"
				} else {
					cancel()
				}
			}
		},
	})
	if !errors.Is(err, context.Canceled) || achieved != 2 || !reflect.DeepEqual(f.actions, []planning.ActionKind{planning.VerifyApp}) {
		t.Fatalf("err=%v achieved=%d actions=%v", err, achieved, f.actions)
	}
}

func TestEnvironmentChangePausesAndRestores(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	current := goal()
	f := &fakeRuntime{after: func(kind planning.ActionKind) {
		if kind == planning.BuildImage {
			current.Environment = "other"
		}
	}}
	invalid := 0
	err := Run(ctx, func(context.Context) (planning.Goal, error) { return current, nil }, f, Options{
		Interval: time.Millisecond,
		Report: func(e Event) {
			if e.Kind == "goal-invalid" {
				invalid++
				if f.observations != 1 || len(f.actions) != 1 {
					t.Fatal("observed or mutated changed environment")
				}
				current.Environment = "demo"
			}
			if e.Kind == "planned" && e.Plan.Status == planning.Achieved {
				cancel()
			}
		},
	})
	if !errors.Is(err, context.Canceled) || invalid != 1 || !reflect.DeepEqual(f.actions, []planning.ActionKind{planning.BuildImage, planning.DeployApp}) {
		t.Fatalf("err=%v invalid=%d actions=%v", err, invalid, f.actions)
	}
}

func TestCancellationWhileWaiting(t *testing.T) {
	for _, invalid := range []bool{false, true} {
		t.Run(map[bool]string{false: "achieved", true: "invalid input"}[invalid], func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			f := &fakeRuntime{state: planning.State{ImageExists: true, AppExists: true, AppCurrent: true, AppReady: true}}
			loads := 0
			err := Run(ctx, func(context.Context) (planning.Goal, error) {
				loads++
				if invalid {
					return planning.Goal{}, errors.New("bad file")
				}
				return goal(), nil
			}, f, Options{Interval: time.Hour, Report: func(e Event) {
				if e.Kind == "goal-invalid" || (e.Kind == "planned" && e.Plan.Status == planning.Achieved) {
					cancel()
				}
			}})
			if !errors.Is(err, context.Canceled) || loads != 1 || len(f.actions) != 0 {
				t.Fatalf("err=%v loads=%d actions=%v", err, loads, f.actions)
			}
			if invalid && f.observations != 0 {
				t.Fatal("observed resources for invalid input")
			}
		})
	}
}
