package main

import (
	"context"
	"io"
	"testing"
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
