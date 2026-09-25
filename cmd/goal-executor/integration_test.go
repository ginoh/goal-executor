package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/ginoh/goal-executor/internal/dockerruntime"
	"github.com/ginoh/goal-executor/internal/planning"
)

type eventWriter struct {
	actions  []string
	achieved bool
	onDeploy func()
}

func (w *eventWriter) Write(p []byte) (int, error) {
	var e struct {
		Event  string
		Action planning.Action
		Plan   planning.Result
		State  planning.State
	}
	if err := json.Unmarshal(p, &e); err != nil {
		return 0, err
	}
	if e.Event == "executing" && e.Action.Kind == planning.DeployAPI && w.onDeploy != nil {
		w.onDeploy()
		w.onDeploy = nil
	}
	if e.Event == "executed" {
		w.actions = append(w.actions, string(e.Action.Kind)+":"+e.Action.APIVersion)
	}
	if e.Event == "planned" && e.Plan.Status == planning.Achieved {
		w.achieved = e.State.TestValid
	}
	return len(p), nil
}

// Explicit opt-in: creates temporary containers/networks in desktop-linux.
// Images must already exist locally. Cleanup does not remove images or volumes.
func TestDockerScenarios(t *testing.T) {
	if os.Getenv("GOAL_EXECUTOR_DOCKER_TEST") != "1" {
		t.Skip("Docker integration test is opt-in")
	}
	image := os.Getenv("GOAL_EXECUTOR_API_IMAGE")
	if image == "" {
		image = "goal-executor-demo:local"
	}
	for _, scenario := range []string{"empty", "reuse", "change"} {
		t.Run(scenario, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
			defer cancel()
			environment := fmt.Sprintf("a-%s-%d", scenario, time.Now().UnixNano())
			r, err := dockerruntime.New(dockerruntime.Config{DBImage: "postgres:17-alpine", APIImage: image, OperationTimeout: 60 * time.Second})
			if err != nil {
				t.Fatal(err)
			}
			s, err := r.Observe(ctx, environment)
			if err != nil || s.DB.Exists || s.API.Exists {
				t.Fatalf("expected empty environment: %+v %v", s, err)
			}
			defer func() {
				if err := run(context.Background(), []string{"cleanup", "--environment", environment}, io.Discard, io.Discard); err != nil {
					t.Errorf("cleanup %s: %v", environment, err)
				}
			}()
			identity := func() string {
				t.Helper()
				out, err := exec.CommandContext(ctx, "docker", "--context", "desktop-linux", "container", "inspect", "--format", "{{.Id}}|{{.State.StartedAt}}", "goal-executor-"+environment+"-db").Output()
				if err != nil {
					t.Fatal(err)
				}
				return string(out)
			}
			var originalDB string
			if scenario == "reuse" {
				for _, a := range []planning.Action{{Kind: planning.CreateDB}, {Kind: planning.InitializeData, Dataset: dockerruntime.Dataset}} {
					if err := r.Execute(ctx, environment, a); err != nil {
						t.Fatal(err)
					}
				}
				originalDB = identity()
			}
			path := filepath.Join(t.TempDir(), "goal.yaml")
			writeGoal := func(version string) error {
				return os.WriteFile(path, []byte(fmt.Sprintf("environment: %s\napiVersion: %s\ndataset: sample-v1\nrequireIntegrationTest: true\n", environment, version)), 0600)
			}
			if err := writeGoal("v1"); err != nil {
				t.Fatal(err)
			}
			writer := &eventWriter{}
			args := []string{"run", "--goal", path, "--api-image", image, "--once", "--operation-timeout", "60s"}
			var changed chan error
			if scenario == "change" {
				args = append(args, "--deploy-delay", "2s")
				writer.onDeploy = func() {
					changed = make(chan error, 1)
					go func() { time.Sleep(200 * time.Millisecond); changed <- writeGoal("v2") }()
				}
			}
			if err := run(ctx, args, writer, io.Discard); err != nil {
				t.Fatal(err)
			}
			if changed != nil {
				if err := <-changed; err != nil {
					t.Fatal(err)
				}
			}
			want := []string{"create-db:", "initialize-data:", "deploy-api:v1", "run-integration-test:v1"}
			if scenario == "reuse" {
				want = want[2:]
				if identity() != originalDB {
					t.Fatal("existing DB was replaced or restarted")
				}
			}
			if scenario == "change" {
				want = []string{"create-db:", "initialize-data:", "deploy-api:v1", "deploy-api:v2", "run-integration-test:v2"}
			}
			if !writer.achieved || !reflect.DeepEqual(writer.actions, want) {
				t.Fatalf("achieved=%v actions=%v want=%v", writer.achieved, writer.actions, want)
			}
			for _, role := range []string{"api", "db"} {
				out, err := exec.CommandContext(ctx, "docker", "--context", "desktop-linux", "container", "inspect", "--format", "{{range .Mounts}}{{if eq .Type \"volume\"}}volume{{end}}{{end}}", "goal-executor-"+environment+"-"+role).Output()
				if err != nil || string(out) != "\n" {
					t.Fatalf("unexpected persistent volume: %q %v", out, err)
				}
			}
			t.Logf("achieved: %v", writer.actions)
		})
	}
}
