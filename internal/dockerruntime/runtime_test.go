package dockerruntime

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ginoh/goal-executor/internal/buildinput"
	"github.com/ginoh/goal-executor/internal/goalfile"
	"github.com/ginoh/goal-executor/internal/planning"
)

type transport func(*http.Request) (*http.Response, error)

func (f transport) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func newTestRuntime(t *testing.T) *Runtime {
	t.Helper()
	body := "hello"
	r, err := New(Config{Environment: "test", Build: buildinput.Snapshot{Dir: "/tmp/build", Dockerfile: "Dockerfile", ID: "abc"}, Application: goalfile.Application{Port: 8080}, Checks: goalfile.Checks{Readiness: goalfile.Check{HTTP: goalfile.HTTPCheck{Path: "/health", Status: 200}}, Verification: goalfile.Check{HTTP: goalfile.HTTPCheck{Path: "/message", Status: 200, BodyEquals: &body}}}, OperationTimeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestCleanupRejectsUnownedBeforeMutation(t *testing.T) {
	r := newTestRuntime(t)
	mutations := 0
	r.command = func(_ context.Context, _ string, args ...string) ([]byte, error) {
		if args[0] == "container" && args[1] == "ls" {
			return []byte("app-id"), nil
		}
		if args[0] == "container" && args[1] == "inspect" {
			return []byte(`{"id":"app-id","owner":"someone-else","environment":"test","role":"app"}`), nil
		}
		mutations++
		return nil, nil
	}
	if err := r.Cleanup(context.Background(), "test"); err == nil || mutations != 0 {
		t.Fatalf("err=%v mutations=%d", err, mutations)
	}
}

func TestEvidenceRequiresSameContainerAndStartTime(t *testing.T) {
	r := newTestRuntime(t)
	id, start, imageID := "app-id", "start-1", "sha256:image-1"
	r.command = func(_ context.Context, _ string, args ...string) ([]byte, error) {
		if args[0] == "image" && args[1] == "ls" {
			return []byte("image-id"), nil
		}
		if args[0] == "image" && args[1] == "inspect" {
			return json.Marshal(image{ID: imageID, Owner: owner, Environment: "test", InputID: "abc"})
		}
		if args[0] == "container" && args[1] == "ls" {
			return []byte("app-id"), nil
		}
		if args[0] == "container" && args[1] == "inspect" {
			return []byte(`{"id":"` + id + `","started":"` + start + `","owner":"` + owner + `","environment":"test","role":"app","running":true,"imageID":"` + imageID + `","port":"8080","ports":{"8080/tcp":[{"HostIP":"127.0.0.1","HostPort":"18080"}]}}`), nil
		}
		return nil, errors.New("unexpected docker command")
	}
	message := "hello"
	r.client = &http.Client{Transport: transport(func(req *http.Request) (*http.Response, error) {
		body := message
		if req.URL.Path == "/health" {
			body = ""
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body))}, nil
	})}
	if err := r.Execute(context.Background(), "test", planning.Action{Kind: planning.VerifyApp}); err != nil {
		t.Fatal(err)
	}
	s, err := r.Observe(context.Background(), "test")
	if err != nil || !s.TestValid {
		t.Fatalf("expected valid evidence: %+v %v", s, err)
	}
	start = "start-2"
	s, err = r.Observe(context.Background(), "test")
	if err != nil || s.TestValid {
		t.Fatalf("restart retained evidence: %+v %v", s, err)
	}
	if err := r.Execute(context.Background(), "test", planning.Action{Kind: planning.VerifyApp}); err != nil {
		t.Fatal(err)
	}
	id = "app-id-new"
	s, err = r.Observe(context.Background(), "test")
	if err != nil || s.TestValid {
		t.Fatalf("replacement retained evidence: %+v %v", s, err)
	}
	message = "wrong"
	if err := r.Execute(context.Background(), "test", planning.Action{Kind: planning.VerifyApp}); err == nil {
		t.Fatal("accepted unexpected verification response")
	}
}

func TestObservationErrorIsNotAbsence(t *testing.T) {
	r := newTestRuntime(t)
	failure := errors.New("daemon unavailable")
	r.command = func(context.Context, string, ...string) ([]byte, error) { return nil, failure }
	if _, err := r.Observe(context.Background(), "test"); !errors.Is(err, failure) {
		t.Fatal(err)
	}
}

func TestInvalidEnvironmentDoesNotReachDocker(t *testing.T) {
	r := newTestRuntime(t)
	r.command = func(context.Context, string, ...string) ([]byte, error) { t.Fatal("called Docker"); return nil, nil }
	for _, env := range []string{"", "--all", "a/b", "other env"} {
		if err := r.Cleanup(context.Background(), env); err == nil {
			t.Fatalf("accepted %q", env)
		}
	}
}

func TestBuildUsesSnapshotDockerfileAndContext(t *testing.T) {
	r := newTestRuntime(t)
	var buildArgs []string
	r.command = func(_ context.Context, _ string, args ...string) ([]byte, error) {
		if args[0] == "image" && args[1] == "ls" {
			return nil, nil
		}
		if args[0] == "container" && args[1] == "ls" {
			return nil, nil
		}
		if args[0] == "image" && args[1] == "build" {
			buildArgs = append([]string(nil), args...)
			return nil, nil
		}
		return nil, errors.New("unexpected command")
	}
	if err := r.Execute(context.Background(), "test", planning.Action{Kind: planning.BuildImage}); err != nil {
		t.Fatal(err)
	}
	for i, arg := range buildArgs {
		if arg == "--file" && i+1 < len(buildArgs) {
			if buildArgs[i+1] != filepath.Join(r.config.Build.Dir, r.config.Build.Dockerfile) {
				t.Fatalf("Dockerfile was not fixed: %v", buildArgs)
			}
			if buildArgs[len(buildArgs)-1] != r.config.Build.Dir {
				t.Fatalf("wrong build context: %v", buildArgs)
			}
			return
		}
	}
	t.Fatalf("missing Dockerfile flag: %v", buildArgs)
}

func TestHTTPCheckUsesOriginalResponse(t *testing.T) {
	for _, status := range []int{http.StatusOK, http.StatusFound} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			r := newTestRuntime(t)
			var app container
			if err := json.Unmarshal([]byte(`{"ports":{"8080/tcp":[{"HostIP":"127.0.0.1","HostPort":"18080"}]}}`), &app); err != nil {
				t.Fatal(err)
			}
			var paths []string
			r.client.Transport = transport(func(req *http.Request) (*http.Response, error) {
				paths = append(paths, req.URL.Path)
				if req.URL.Path == "/original" {
					return &http.Response{StatusCode: http.StatusFound, Header: http.Header{"Location": []string{"/target"}}, Body: io.NopCloser(strings.NewReader("redirect"))}, nil
				}
				return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader("hello"))}, nil
			})
			body := "redirect"
			if status == http.StatusOK {
				body = "hello"
			}
			passed, err := r.check(context.Background(), app, goalfile.HTTPCheck{Path: "/original", Status: status, BodyEquals: &body})
			if err != nil || passed != (status == http.StatusFound) {
				t.Fatalf("passed=%v err=%v", passed, err)
			}
			if len(paths) != 1 || paths[0] != "/original" {
				t.Fatalf("followed redirect: %v", paths)
			}
		})
	}
}
