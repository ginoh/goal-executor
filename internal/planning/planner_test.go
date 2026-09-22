package planning

import (
	"reflect"
	"testing"
)

func testGoal() Goal {
	return Goal{Environment: "demo", APIVersion: "v2", Dataset: "sample-v1", RequireIntegrationTest: true}
}

func readyState(version string, tested bool) State {
	return State{
		DB:        DBState{Exists: true, Ready: true, Dataset: "sample-v1"},
		API:       APIState{Exists: true, Ready: true, Version: version},
		TestValid: tested,
	}
}

func kinds(actions []Action) []ActionKind {
	var result []ActionKind
	for _, action := range actions {
		result = append(result, action.Kind)
	}
	return result
}

func TestPlanScenarios(t *testing.T) {
	tests := []struct {
		name   string
		state  State
		status Status
		want   []ActionKind
	}{
		{"empty environment", State{}, Planned, []ActionKind{CreateDB, InitializeData, DeployAPI, RunIntegrationTest}},
		{"existing empty DB", State{DB: DBState{Exists: true, Ready: true}}, Planned, []ActionKind{InitializeData, DeployAPI, RunIntegrationTest}},
		{"reuse initialized DB", State{DB: DBState{Exists: true, Ready: true, Dataset: "sample-v1"}}, Planned, []ActionKind{DeployAPI, RunIntegrationTest}},
		{"changed version invalidates old test", readyState("v1", true), Planned, []ActionKind{DeployAPI, RunIntegrationTest}},
		{"test pending", readyState("v2", false), Planned, []ActionKind{RunIntegrationTest}},
		{"achieved", readyState("v2", true), Achieved, nil},
		{"different dataset is not overwritten", State{DB: DBState{Exists: true, Ready: true, Dataset: "other"}}, Unreachable, nil},
		{"unready DB has no recovery operation", State{DB: DBState{Exists: true}}, Unreachable, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			before := tt.state
			result, err := Plan(testGoal(), tt.state, Options{})
			if err != nil {
				t.Fatal(err)
			}
			if result.Status != tt.status || !reflect.DeepEqual(kinds(result.Actions), tt.want) {
				t.Fatalf("got %s %v, want %s %v", result.Status, kinds(result.Actions), tt.status, tt.want)
			}
			if tt.state != before {
				t.Fatal("planning changed the observation")
			}
			if result.Reason == "" {
				t.Fatal("missing result explanation")
			}
			for _, action := range result.Actions {
				if action.Reason == "" {
					t.Fatalf("missing explanation for %s", action.Kind)
				}
				if action.Kind == DeployAPI || action.Kind == RunIntegrationTest {
					if action.APIVersion != "v2" {
						t.Fatalf("wrong API target: %+v", action)
					}
				}
				if action.Kind == InitializeData || action.Kind == RunIntegrationTest {
					if action.Dataset != "sample-v1" {
						t.Fatalf("wrong dataset target: %+v", action)
					}
				}
			}
		})
	}
}

func TestPlanWithoutIntegrationTest(t *testing.T) {
	goal := testGoal()
	goal.RequireIntegrationTest = false
	for _, state := range []State{{}, readyState("v2", false)} {
		result, err := Plan(goal, state, Options{})
		if err != nil {
			t.Fatal(err)
		}
		if state.API.Ready {
			if result.Status != Achieved {
				t.Fatalf("got %+v", result)
			}
		} else if !reflect.DeepEqual(kinds(result.Actions), []ActionKind{CreateDB, InitializeData, DeployAPI}) {
			t.Fatalf("unexpected actions: %+v", result)
		}
	}
}

func TestReplanUsesNewGoalWithoutChangingOldPlan(t *testing.T) {
	goal := testGoal()
	goal.APIVersion = "v1"
	initial := State{DB: DBState{Exists: true, Ready: true, Dataset: "sample-v1"}}
	old, err := Plan(goal, initial, Options{})
	if err != nil {
		t.Fatal(err)
	}
	goal.APIVersion = "v2"
	// Represents a new observation after the in-flight v1 deployment completes.
	next, err := Plan(goal, readyState("v1", false), Options{})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(kinds(next.Actions), []ActionKind{DeployAPI, RunIntegrationTest}) {
		t.Fatalf("unexpected replan: %+v", next)
	}
	if old.Actions[0].APIVersion != "v1" || next.Actions[0].APIVersion != "v2" {
		t.Fatalf("targets changed incorrectly: old=%+v next=%+v", old, next)
	}
}

func TestPredictionsInvalidateEvidence(t *testing.T) {
	// The stale flag deliberately exercises each effect independently of input
	// validation; predictions must never carry old evidence into a new resource.
	for _, op := range operations(testGoal()) {
		if op.action.Kind == RunIntegrationTest {
			continue
		}
		before := readyState("v1", true)
		after := op.predict(before)
		if after.TestValid {
			t.Fatalf("%s retained old evidence", op.action.Kind)
		}
		if !before.TestValid {
			t.Fatal("prediction modified input")
		}
	}
}

func TestPlanSearchBoundaries(t *testing.T) {
	for _, tt := range []struct {
		name  string
		state State
		limit int
		want  Status
	}{
		{"initial state counts", State{}, 1, LimitReached},
		{"goal would exceed bound", State{}, 4, LimitReached},
		{"goal fits bound", State{}, 5, Planned},
		{"achieved at bound", readyState("v2", true), 1, Achieved},
		{"exhausted at bound", State{DB: DBState{Exists: true}}, 1, Unreachable},
	} {
		t.Run(tt.name, func(t *testing.T) {
			result, err := Plan(testGoal(), tt.state, Options{MaxStates: tt.limit})
			if err != nil {
				t.Fatal(err)
			}
			if result.Status != tt.want {
				t.Fatalf("got %+v, want %s", result, tt.want)
			}
			if result.Status != Planned && len(result.Actions) != 0 {
				t.Fatal("returned a partial plan")
			}
		})
	}
}

func TestPlanRejectsInvalidInput(t *testing.T) {
	for _, tt := range []struct {
		name   string
		change func(*Goal, *State, *Options)
	}{
		{"missing environment", func(g *Goal, _ *State, _ *Options) { g.Environment = " " }},
		{"missing version", func(g *Goal, _ *State, _ *Options) { g.APIVersion = "" }},
		{"missing dataset", func(g *Goal, _ *State, _ *Options) { g.Dataset = "" }},
		{"negative limit", func(_ *Goal, _ *State, o *Options) { o.MaxStates = -1 }},
		{"absent ready DB", func(_ *Goal, s *State, _ *Options) { s.DB.Ready = true }},
		{"absent initialized DB", func(_ *Goal, s *State, _ *Options) { s.DB.Dataset = "sample-v1" }},
		{"absent ready API", func(_ *Goal, s *State, _ *Options) { s.API.Ready = true }},
		{"absent versioned API", func(_ *Goal, s *State, _ *Options) { s.API.Version = "v1" }},
		{"ready API without version", func(_ *Goal, s *State, _ *Options) { s.API = APIState{Exists: true, Ready: true} }},
		{"evidence without resources", func(_ *Goal, s *State, _ *Options) { s.TestValid = true }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			goal, state, options := testGoal(), State{}, Options{}
			tt.change(&goal, &state, &options)
			if result, err := Plan(goal, state, options); err == nil || len(result.Actions) != 0 {
				t.Fatalf("got result=%+v err=%v", result, err)
			}
		})
	}
}

// Graph fixtures test the search independently of the API/DB operation chain.
func edge(from, to string, kind ActionKind) operation {
	return operation{
		action:     Action{Kind: kind},
		applicable: func(s State) bool { return s.DB.Dataset == from },
		predict:    func(s State) State { s.DB.Dataset = to; return s },
	}
}

func TestSearchChoosesShortestPathAndStableTie(t *testing.T) {
	ops := []operation{
		edge("", "long", "long-start"),
		edge("long", "middle", "long-middle"),
		edge("middle", "goal", "long-end"),
		edge("", "short", "short-start"),
		edge("short", "goal", "short-end"),
		edge("", "other", "other-start"),
		edge("other", "goal", "other-end"),
	}
	result := search(State{}, func(s State) bool { return s.DB.Dataset == "goal" }, ops, 10)
	if result.Status != Planned || !reflect.DeepEqual(kinds(result.Actions), []ActionKind{"short-start", "short-end"}) {
		t.Fatalf("unexpected shortest path: %+v", result)
	}
}

func TestSearchDeduplicatesCyclesAndSelfLoops(t *testing.T) {
	ops := []operation{edge("", "", "self"), edge("", "next", "forward"), edge("next", "", "back")}
	result := search(State{}, func(s State) bool { return s.DB.Dataset == "missing" }, ops, 2)
	if result.Status != Unreachable {
		t.Fatalf("cycle was not exhausted within two states: %+v", result)
	}
}
