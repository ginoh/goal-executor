package dockerruntime

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/ginoh/goal-executor/internal/planning"
)

type transport func(*http.Request) (*http.Response, error)

func (f transport) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func newTestRuntime(t *testing.T) *Runtime {
	t.Helper()
	r, err := New(Config{DBImage: "postgres:test", APIImage: "api:test", OperationTimeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestCleanupRejectsUnownedResourcesBeforeMutation(t *testing.T) {
	for _, unowned := range []string{"api", "db", "net"} {
		t.Run(unowned, func(t *testing.T) {
			r := newTestRuntime(t)
			mutations := 0
			r.command = func(_ context.Context, _ string, args ...string) ([]byte, error) {
				if args[0] == "network" {
					if args[1] == "ls" {
						return []byte("network-id"), nil
					}
					if args[1] == "inspect" {
						return []byte("different-owner|test|net"), nil
					}
				}
				if args[0] == "container" && args[1] == "ls" {
					if strings.Contains(strings.Join(args, " "), "-api$") {
						return []byte("api-id"), nil
					}
					return []byte("db-id"), nil
				}
				if args[0] == "container" && args[1] == "inspect" {
					role := strings.TrimSuffix(args[len(args)-1], "-id")
					c := container{ID: role + "-id", Owner: owner, Environment: "test", Role: role, Running: true}
					if role == unowned {
						c.Owner = "someone-else"
					}
					return json.Marshal(c)
				}
				mutations++
				return nil, nil
			}
			if err := r.Cleanup(context.Background(), "test"); err == nil || mutations != 0 {
				t.Fatalf("err=%v mutations=%d", err, mutations)
			}
		})
	}
}

func TestObservationFailureIsNotAbsence(t *testing.T) {
	r := newTestRuntime(t)
	failure := errors.New("daemon unavailable")
	r.command = func(context.Context, string, ...string) ([]byte, error) { return nil, failure }
	if _, err := r.Observe(context.Background(), "test"); !errors.Is(err, failure) {
		t.Fatalf("got %v", err)
	}
}

func TestEvidenceMatchesResourceIdentityAndData(t *testing.T) {
	r := newTestRuntime(t)
	dbID, apiID, dbStart, apiStart := "db-id", "api-id", "db-start", "api-start"
	data := Data{Version: "v1", Dataset: Dataset, Generation: "generation-1", Value: SampleValue}
	r.command = func(_ context.Context, _ string, args ...string) ([]byte, error) {
		if args[0] == "container" && args[1] == "ls" {
			if strings.Contains(strings.Join(args, " "), "-api$") {
				return []byte("api"), nil
			}
			return []byte("db"), nil
		}
		if args[0] == "container" && args[1] == "inspect" {
			role := args[len(args)-1]
			c := container{ID: dbID, Started: dbStart, Owner: owner, Environment: "test", Role: role, Running: true}
			if role == "api" {
				// JSON keeps the fixture independent of the anonymous ports type.
				return []byte(`{"id":"` + apiID + `","started":"` + apiStart + `","owner":"` + owner + `","environment":"test","role":"api","running":true,"ports":{"8080/tcp":[{"HostIP":"127.0.0.1","HostPort":"18080"}]}}`), nil
			}
			return json.Marshal(c)
		}
		if args[0] == "exec" && args[2] == "pg_isready" {
			return nil, nil
		}
		if args[0] == "exec" {
			return json.Marshal(data)
		}
		return nil, errors.New("unexpected command")
	}
	baseCommand := r.command
	r.command = func(ctx context.Context, input string, args ...string) ([]byte, error) {
		if strings.Contains(input, "to_regclass") {
			return []byte("t"), nil
		}
		return baseCommand(ctx, input, args...)
	}
	r.client = &http.Client{Transport: transport(func(*http.Request) (*http.Response, error) {
		body, _ := json.Marshal(data)
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(string(body)))}, nil
	})}
	testAction := planning.Action{Kind: planning.RunIntegrationTest, APIVersion: "v1", Dataset: Dataset}
	for _, change := range []struct {
		name  string
		apply func()
	}{
		{"API replacement", func() { apiID += "-new" }},
		{"DB replacement", func() { dbID += "-new" }},
		{"API restart", func() { apiStart += "-new" }},
		{"DB restart", func() { dbStart += "-new" }},
		{"data generation", func() { data.Generation += "-new" }},
	} {
		t.Run(change.name, func(t *testing.T) {
			if err := r.Execute(context.Background(), "test", testAction); err != nil {
				t.Fatal(err)
			}
			s, err := r.Observe(context.Background(), "test")
			if err != nil || !s.TestValid {
				t.Fatalf("before change: %+v %v", s, err)
			}
			change.apply()
			s, err = r.Observe(context.Background(), "test")
			if err != nil || s.TestValid {
				t.Fatalf("stale evidence retained: %+v %v", s, err)
			}
		})
	}
	data.Value = "incorrect"
	if err := r.Execute(context.Background(), "test", testAction); err == nil {
		t.Fatal("accepted wrong data")
	}
}

func TestEnvironmentValidationBeforeDocker(t *testing.T) {
	r := newTestRuntime(t)
	r.command = func(context.Context, string, ...string) ([]byte, error) {
		t.Fatal("Docker called for invalid name")
		return nil, nil
	}
	for _, environment := range []string{"", "--all", "a/b", "other env", strings.Repeat("a", 41)} {
		if err := r.Cleanup(context.Background(), environment); err == nil {
			t.Fatalf("accepted %q", environment)
		}
	}
}
