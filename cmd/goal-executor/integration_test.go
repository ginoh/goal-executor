package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Explicit opt-in: builds an image and creates one temporary app container in desktop-linux.
func TestDockerScenario(t *testing.T) {
	if os.Getenv("GOAL_EXECUTOR_DOCKER_TEST") != "1" {
		t.Skip("Docker integration test is opt-in")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	env := fmt.Sprintf("a-stage1-%d", time.Now().UnixNano())
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "goal.yaml")
	// The input path remains relative to the temporary goal file.
	rel, err := filepath.Rel(filepath.Dir(path), root)
	if err != nil {
		t.Fatal(err)
	}
	config := fmt.Sprintf("environment: %s\napplication:\n  build:\n    context: %s\n    dockerfile: examples/demo/Dockerfile\n  port: 8080\nchecks:\n  readiness:\n    http: {path: /health, status: 200}\n  verification:\n    http: {path: /message, status: 200, bodyEquals: hello}\ngoal:\n  state: verified\n", env, rel)
	if err := os.WriteFile(path, []byte(config), 0600); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := run(context.Background(), []string{"cleanup", "--environment", env}, io.Discard, io.Discard); err != nil {
			t.Errorf("cleanup: %v", err)
		}
	}()
	var first bytes.Buffer
	if err := run(ctx, []string{"run", "--goal", path, "--once"}, &first, io.Discard); err != nil {
		t.Fatal(err)
	}
	for _, action := range []string{"build-image", "deploy-app", "verify-app"} {
		if !bytes.Contains(first.Bytes(), []byte(`"Kind":"`+action+`"`)) {
			t.Fatalf("first run omitted %s", action)
		}
	}
	var second bytes.Buffer
	if err := run(ctx, []string{"run", "--goal", path, "--once"}, &second, io.Discard); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(second.Bytes(), []byte(`"Kind":"build-image"`)) || bytes.Contains(second.Bytes(), []byte(`"Kind":"deploy-app"`)) || !bytes.Contains(second.Bytes(), []byte(`"Kind":"verify-app"`)) {
		t.Fatalf("second run did not reuse image and container: %s", second.String())
	}
}
