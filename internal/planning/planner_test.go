package planning

import (
	"reflect"
	"testing"
)

func testGoal() Goal { return Goal{Environment: "demo", InputID: "abc", State: "verified"} }
func kinds(actions []Action) []ActionKind {
	var out []ActionKind
	for _, a := range actions {
		out = append(out, a.Kind)
	}
	return out
}

func TestPlanScenarios(t *testing.T) {
	for _, tt := range []struct {
		name   string
		state  State
		goal   string
		status Status
		want   []ActionKind
	}{
		{"empty", State{}, "verified", Planned, []ActionKind{BuildImage, DeployApp, VerifyApp}},
		{"image reuse", State{ImageExists: true}, "verified", Planned, []ActionKind{DeployApp, VerifyApp}},
		{"outdated app", State{ImageExists: true, AppExists: true}, "verified", Planned, []ActionKind{DeployApp, VerifyApp}},
		{"ready goal", State{ImageExists: true, AppExists: true, AppCurrent: true, AppReady: true}, "ready", Achieved, nil},
		{"verify only", State{ImageExists: true, AppExists: true, AppCurrent: true, AppReady: true}, "verified", Planned, []ActionKind{VerifyApp}},
		{"verified", State{ImageExists: true, AppExists: true, AppCurrent: true, AppReady: true, TestValid: true}, "verified", Achieved, nil},
	} {
		t.Run(tt.name, func(t *testing.T) {
			g := testGoal()
			g.State = tt.goal
			before := tt.state
			result, err := Plan(g, tt.state, Options{})
			if err != nil || result.Status != tt.status || !reflect.DeepEqual(kinds(result.Actions), tt.want) {
				t.Fatalf("result=%+v err=%v", result, err)
			}
			if tt.state != before || result.Reason == "" {
				t.Fatal("planner mutated observation or omitted reason")
			}
		})
	}
}

func TestPredictionsInvalidateEvidence(t *testing.T) {
	for _, op := range operations(testGoal()) {
		if op.action.Kind == VerifyApp {
			continue
		}
		before := State{ImageExists: true, AppExists: true, AppCurrent: true, AppReady: true, TestValid: true}
		after := op.predict(before)
		if op.action.Kind == DeployApp && (after.TestValid || !before.TestValid) {
			t.Fatal("deployment retained evidence")
		}
	}
}

func TestPlanBoundAndValidation(t *testing.T) {
	if result, _ := Plan(testGoal(), State{}, Options{MaxStates: 1}); result.Status != LimitReached {
		t.Fatal(result)
	}
	if result, _ := Plan(testGoal(), State{}, Options{MaxStates: 4}); result.Status != Planned {
		t.Fatal(result)
	}
	for _, state := range []State{{AppCurrent: true}, {AppReady: true}, {TestValid: true}} {
		if _, err := Plan(testGoal(), state, Options{}); err == nil {
			t.Fatalf("accepted %+v", state)
		}
	}
	g := testGoal()
	g.State = "other"
	if _, err := Plan(g, State{}, Options{}); err == nil {
		t.Fatal("accepted bad goal")
	}
}

func edge(from, to bool, kind ActionKind) operation {
	return operation{Action{Kind: kind}, func(s State) bool { return s.ImageExists == from }, func(s State) State { s.ImageExists = to; return s }}
}
func TestSearchDeduplicatesCycles(t *testing.T) {
	ops := []operation{edge(false, false, "self"), edge(false, true, "forward"), edge(true, false, "back")}
	result := search(State{}, func(State) bool { return false }, ops, 2)
	if result.Status != Unreachable {
		t.Fatal(result)
	}
}

func TestSearchChoosesShortestPathAndStableTie(t *testing.T) {
	// Arbitrary graph nodes exercise the search independently of application operations.
	start := State{}
	long := State{ImageExists: true}
	middle := State{AppExists: true}
	short := State{AppCurrent: true}
	other := State{AppReady: true}
	end := State{TestValid: true}
	graphEdge := func(from, to State, kind ActionKind) operation {
		return operation{Action{Kind: kind}, func(s State) bool { return s == from }, func(State) State { return to }}
	}
	ops := []operation{
		graphEdge(start, long, "long-start"), graphEdge(long, middle, "long-middle"), graphEdge(middle, end, "long-end"),
		graphEdge(start, short, "short-start"), graphEdge(short, end, "short-end"),
		graphEdge(start, other, "other-start"), graphEdge(other, end, "other-end"),
	}
	result := search(start, func(s State) bool { return s == end }, ops, 10)
	if result.Status != Planned || !reflect.DeepEqual(kinds(result.Actions), []ActionKind{"short-start", "short-end"}) {
		t.Fatalf("unexpected shortest path or tie order: %+v", result)
	}
}

func TestPlanSearchBoundaries(t *testing.T) {
	ready := State{ImageExists: true, AppExists: true, AppCurrent: true, AppReady: true, TestValid: true}
	for _, tt := range []struct {
		name  string
		state State
		limit int
		want  Status
	}{
		{"initial state counts", State{}, 1, LimitReached},
		{"goal exceeds bound", State{}, 3, LimitReached},
		{"goal fits bound", State{}, 4, Planned},
		{"achieved at bound", ready, 1, Achieved},
	} {
		t.Run(tt.name, func(t *testing.T) {
			result, err := Plan(testGoal(), tt.state, Options{MaxStates: tt.limit})
			if err != nil || result.Status != tt.want {
				t.Fatalf("result=%+v err=%v", result, err)
			}
			if result.Status != Planned && len(result.Actions) != 0 {
				t.Fatal("returned a partial plan")
			}
		})
	}
}
