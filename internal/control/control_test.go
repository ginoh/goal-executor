package control

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/ginoh/goal-executor/internal/goalfile"
	"github.com/ginoh/goal-executor/internal/planning"
)

type fakeRuntime struct {
	state        planning.State
	actions      []planning.Action
	observations int
	observeErr   error
	executeErr   error
	after        func(planning.Action)
	noEffect     bool
}

func (f *fakeRuntime) Observe(context.Context, string) (planning.State, error) {
	f.observations++
	return f.state, f.observeErr
}

func (f *fakeRuntime) Execute(_ context.Context, _ string, action planning.Action) error {
	f.actions = append(f.actions, action)
	if f.executeErr != nil {
		return f.executeErr
	}
	if !f.noEffect {
		switch action.Kind {
		case planning.CreateDB:
			f.state.DB = planning.DBState{Exists: true, Ready: true}
		case planning.InitializeData:
			f.state.DB.Dataset = action.Dataset
		case planning.DeployAPI:
			f.state.API = planning.APIState{Exists: true, Ready: true, Version: action.APIVersion}
			f.state.TestValid = false
		case planning.RunIntegrationTest:
			f.state.TestValid = true
		}
	}
	if f.after != nil {
		f.after(action)
	}
	return nil
}

func goal() planning.Goal {
	return planning.Goal{Environment: "demo", APIVersion: "v1", Dataset: "sample-v1", RequireIntegrationTest: true}
}

func loader(context.Context) (planning.Goal, error) { return goal(), nil }

func boundedContext(t *testing.T) (context.Context, context.CancelFunc) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	t.Cleanup(cancel)
	return ctx, cancel
}

func TestFileGoalChangeReplansAfterOperation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "goal.yaml")
	write := func(version string) {
		t.Helper()
		data := "environment: demo\napiVersion: " + version + "\ndataset: sample-v1\nrequireIntegrationTest: true\n"
		if err := os.WriteFile(path, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write("v1")
	ctx, cancel := boundedContext(t)
	f := &fakeRuntime{}
	f.after = func(action planning.Action) {
		// The v1 operation has received its arguments, but has not returned yet.
		if action.Kind == planning.DeployAPI && action.APIVersion == "v1" {
			write("v2")
		}
	}
	var statuses []planning.Status
	err := Run(ctx, func(context.Context) (planning.Goal, error) { return goalfile.Load(path) }, f, Options{
		Report: func(e Event) {
			if e.Kind == "planned" {
				statuses = append(statuses, e.Plan.Status)
				if e.Plan.Status == planning.Achieved {
					cancel()
				}
			}
		},
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("unexpected stop: %v", err)
	}
	var kinds []planning.ActionKind
	for _, a := range f.actions {
		kinds = append(kinds, a.Kind)
	}
	want := []planning.ActionKind{planning.CreateDB, planning.InitializeData, planning.DeployAPI, planning.DeployAPI, planning.RunIntegrationTest}
	if !reflect.DeepEqual(kinds, want) {
		t.Fatalf("got %v", kinds)
	}
	if f.actions[2].APIVersion != "v1" || f.actions[3].APIVersion != "v2" || f.actions[4].APIVersion != "v2" {
		t.Fatalf("wrong operation targets: %+v", f.actions)
	}
	if f.observations != len(f.actions)+1 || statuses[len(statuses)-1] != planning.Achieved {
		t.Fatalf("observation/achievement mismatch: %d %v", f.observations, statuses)
	}
}

func TestInvalidFilePausesAndRecovers(t *testing.T) {
	path := filepath.Join(t.TempDir(), "goal.yaml")
	if err := os.WriteFile(path, []byte("apiVersion: ["), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := boundedContext(t)
	f := &fakeRuntime{}
	invalid := 0
	err := Run(ctx, func(context.Context) (planning.Goal, error) { return goalfile.Load(path) }, f, Options{
		Interval: time.Millisecond,
		Report: func(e Event) {
			if e.Kind == "goal-invalid" {
				invalid++
				if f.observations != 0 || len(f.actions) != 0 {
					t.Fatal("used invalid goal")
				}
				data := "environment: demo\napiVersion: v1\ndataset: sample-v1\nrequireIntegrationTest: true\n"
				if err := os.WriteFile(path, []byte(data), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if e.Kind == "planned" && e.Plan.Status == planning.Achieved {
				cancel()
			}
		},
	})
	if !errors.Is(err, context.Canceled) || invalid != 1 || len(f.actions) != 4 {
		t.Fatalf("err=%v invalid=%d actions=%v", err, invalid, f.actions)
	}
}

func TestStopsOnRuntimeAndPlanningFailures(t *testing.T) {
	failure := errors.New("runtime failed")
	for _, tt := range []struct {
		name    string
		runtime fakeRuntime
		options Options
		want    string
		actions int
	}{
		{"observe", fakeRuntime{observeErr: failure}, Options{}, "observe environment", 0},
		{"execute", fakeRuntime{executeErr: failure}, Options{}, "execute create-db", 1},
		{"unreachable", fakeRuntime{state: planning.State{DB: planning.DBState{Exists: true}}}, Options{}, "unreachable", 0},
		{"limit", fakeRuntime{}, Options{Planner: planning.Options{MaxStates: 1}}, "limit-reached", 0},
		{"invalid observation", fakeRuntime{state: planning.State{DB: planning.DBState{Ready: true}}}, Options{}, "plan:", 0},
	} {
		t.Run(tt.name, func(t *testing.T) {
			ctx, _ := boundedContext(t)
			err := Run(ctx, loader, &tt.runtime, tt.options)
			if err == nil || !strings.Contains(err.Error(), tt.want) || len(tt.runtime.actions) != tt.actions {
				t.Fatalf("err=%v actions=%v", err, tt.runtime.actions)
			}
			if tt.runtime.observeErr != nil || tt.runtime.executeErr != nil {
				if !errors.Is(err, failure) {
					t.Fatal("lost underlying error")
				}
			}
		})
	}
}

func TestAchievementWaitsAndReadsNewGoal(t *testing.T) {
	ctx, cancel := boundedContext(t)
	f := &fakeRuntime{state: planning.State{
		DB:  planning.DBState{Exists: true, Ready: true, Dataset: "sample-v1"},
		API: planning.APIState{Exists: true, Ready: true, Version: "v1"}, TestValid: true,
	}}
	current := goal()
	achieved := 0
	err := Run(ctx, func(context.Context) (planning.Goal, error) { return current, nil }, f, Options{
		Interval: time.Millisecond,
		Report: func(e Event) {
			if e.Kind == "planned" && e.Plan.Status == planning.Achieved {
				achieved++
				if achieved == 1 {
					current.APIVersion = "v2"
				} else {
					cancel()
				}
			}
		},
	})
	if !errors.Is(err, context.Canceled) || achieved != 2 || len(f.actions) != 2 || f.actions[0].APIVersion != "v2" {
		t.Fatalf("err=%v achieved=%d actions=%v", err, achieved, f.actions)
	}
}

func TestExecutionSuccessDoesNotReplaceObservation(t *testing.T) {
	ctx, cancel := boundedContext(t)
	f := &fakeRuntime{noEffect: true}
	f.after = func(planning.Action) {
		if len(f.actions) == 2 {
			cancel()
		}
	}
	err := Run(ctx, loader, f, Options{})
	if !errors.Is(err, context.Canceled) || f.observations != 2 || len(f.actions) != 2 || f.actions[1].Kind != planning.CreateDB {
		t.Fatalf("used predicted state as observation: err=%v runtime=%+v", err, f)
	}
}

func TestEnvironmentChangePausesOperations(t *testing.T) {
	ctx, cancel := boundedContext(t)
	current := goal()
	f := &fakeRuntime{after: func(planning.Action) { current.Environment = "other" }}
	err := Run(ctx, func(context.Context) (planning.Goal, error) { return current, nil }, f, Options{
		Report: func(e Event) {
			if e.Kind == "goal-invalid" {
				cancel()
			}
		},
	})
	if !errors.Is(err, context.Canceled) || len(f.actions) != 1 || f.observations != 1 {
		t.Fatalf("err=%v runtime=%+v", err, f)
	}
}

func TestCancellationWhileWaiting(t *testing.T) {
	ctx, cancel := boundedContext(t)
	f := &fakeRuntime{}
	err := Run(ctx, func(context.Context) (planning.Goal, error) { return planning.Goal{}, errors.New("bad file") }, f, Options{
		Interval: time.Hour,
		Report:   func(Event) { cancel() },
	})
	if !errors.Is(err, context.Canceled) || f.observations != 0 {
		t.Fatalf("err=%v observations=%d", err, f.observations)
	}
}
