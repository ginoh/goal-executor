package main

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ginoh/goal-executor/internal/goalfile"
)

func TestInvalidArgumentsDoNotReachDocker(t *testing.T) {
	for _, args := range [][]string{nil, {"unknown"}, {"run"}, {"cleanup"}, {"run", "--goal", "goal.yaml", "--interval", "-1s"}, {"cleanup", "--environment", "--all"}, {"run", "--goal", "goal.yaml", "--operation-timeout", "0s"}} {
		if err := run(context.Background(), args, io.Discard, io.Discard); err == nil {
			t.Fatalf("accepted %v", args)
		}
	}
}

func TestHelp(t *testing.T) {
	if err := run(context.Background(), []string{"run", "--help"}, io.Discard, io.Discard); err != nil {
		t.Fatal(err)
	}
}

func TestFixedConfigurationAllowsGoalChange(t *testing.T) {
	path := filepath.Join(t.TempDir(), "goal.yaml")
	initialYAML := "environment: demo\napplication:\n  build: {context: '.', dockerfile: Dockerfile}\n  port: 8080\nchecks:\n  readiness:\n    http: {path: /health, status: 200}\n  verification:\n    http: {path: /message, status: 200, bodyEquals: hello}\ngoal: {state: ready}\n"
	write := func(data string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write(initialYAML)
	initial, err := goalfile.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	load := fixedGoalLoader(path, initial, "abc")
	write(strings.Replace(initialYAML, "state: ready", "state: verified", 1))
	if g, err := load(context.Background()); err != nil || g.State != "verified" {
		t.Fatalf("goal=%+v err=%v", g, err)
	}
	write("# comment does not change configuration\n" + initialYAML)
	if _, err := load(context.Background()); err != nil {
		t.Fatalf("comment changed configuration: %v", err)
	}
	write(strings.Replace(initialYAML, "port: 8080", "port: 8081", 1))
	if _, err := load(context.Background()); err == nil {
		t.Fatal("accepted changed app configuration")
	}
	write(initialYAML)
	if _, err := load(context.Background()); err != nil {
		t.Fatalf("did not resume after restore: %v", err)
	}
}
