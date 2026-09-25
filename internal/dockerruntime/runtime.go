// Package dockerruntime implements one local application environment with Docker CLI.
package dockerruntime

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/ginoh/goal-executor/internal/buildinput"
	"github.com/ginoh/goal-executor/internal/goalfile"
	"github.com/ginoh/goal-executor/internal/planning"
)

const owner = "goal-executor-cli-v2"
const labelPrefix = "io.goal-executor."

var environmentPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,39}$`)
var portPattern = regexp.MustCompile(`^[0-9]+$`)

func ValidateEnvironment(environment string) error {
	if !environmentPattern.MatchString(environment) {
		return errors.New("environment must match [a-z][a-z0-9-]{0,39}")
	}
	return nil
}

type Config struct {
	Environment      string
	Build            buildinput.Snapshot
	Application      goalfile.Application
	Checks           goalfile.Checks
	OperationTimeout time.Duration
}
type command func(context.Context, string, ...string) ([]byte, error)
type fingerprint struct{ ID, Started, CheckID string }
type Runtime struct {
	config   Config
	command  command
	client   *http.Client
	evidence map[string]fingerprint
}

func New(config Config) (*Runtime, error) {
	if err := ValidateEnvironment(config.Environment); err != nil {
		return nil, err
	}
	if config.OperationTimeout <= 0 {
		return nil, errors.New("positive timeout is required")
	}
	if config.Build.Dir != "" && (config.Build.ID == "" || config.Build.Dockerfile == "" || config.Application.Port < 1 || config.Application.Port > 65535) {
		return nil, errors.New("valid build and port are required")
	}
	return &Runtime{
		config: config, command: dockerCommand, evidence: map[string]fingerprint{},
		client: &http.Client{
			Timeout: 3 * time.Second,
			// Checks evaluate the configured endpoint, including its own 3xx response.
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
	}, nil
}

func dockerCommand(ctx context.Context, input string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "docker", append([]string{"--context", "desktop-linux"}, args...)...)
	cmd.Stdin = strings.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("docker %s failed: %w", args[0], err)
	}
	return bytes.TrimSpace(out), nil
}

func name(environment string) string { return "goal-executor-" + environment + "-app" }
func imageName(environment, inputID string) string {
	return "goal-executor-" + environment + ":" + inputID
}
func labels(environment string) []string {
	return []string{"--label", labelPrefix + "owner=" + owner, "--label", labelPrefix + "environment=" + environment, "--label", labelPrefix + "role=app"}
}

type container struct {
	ID          string `json:"id"`
	Running     bool   `json:"running"`
	Started     string `json:"started"`
	Owner       string `json:"owner"`
	Environment string `json:"environment"`
	Role        string `json:"role"`
	ImageID     string `json:"imageID"`
	Port        string `json:"port"`
	Ports       map[string][]struct {
		HostIP   string
		HostPort string
	} `json:"ports"`
}

const containerFormat = `{"id":{{json .Id}},"running":{{json .State.Running}},"started":{{json .State.StartedAt}},"owner":{{json (index .Config.Labels "io.goal-executor.owner")}},"environment":{{json (index .Config.Labels "io.goal-executor.environment")}},"role":{{json (index .Config.Labels "io.goal-executor.role")}},"imageID":{{json .Image}},"port":{{json (index .Config.Labels "io.goal-executor.port")}},"ports":{{json .NetworkSettings.Ports}}}`

type image struct {
	ID          string `json:"id"`
	Owner       string `json:"owner"`
	Environment string `json:"environment"`
	InputID     string `json:"inputID"`
}

const imageFormat = `{"id":{{json .Id}},"owner":{{json (index .Config.Labels "io.goal-executor.owner")}},"environment":{{json (index .Config.Labels "io.goal-executor.environment")}},"inputID":{{json (index .Config.Labels "io.goal-executor.input")}}}`

func (r *Runtime) inspectApp(ctx context.Context) (container, error) {
	var c container
	out, err := r.command(ctx, "", "container", "ls", "-a", "--filter", "name=^/"+name(r.config.Environment)+"$", "--format", "{{.ID}}")
	if err != nil {
		return c, err
	}
	if len(out) == 0 {
		return c, nil
	}
	if bytes.Contains(out, []byte("\n")) {
		return c, errors.New("ambiguous container identity")
	}
	out, err = r.command(ctx, "", "container", "inspect", "--format", containerFormat, string(out))
	if err != nil {
		return c, err
	}
	if err := json.Unmarshal(out, &c); err != nil {
		return c, fmt.Errorf("decode app observation: %w", err)
	}
	if c.Owner != owner || c.Environment != r.config.Environment || c.Role != "app" {
		return c, errors.New("refusing unowned app container")
	}
	return c, nil
}

func (r *Runtime) inspectImage(ctx context.Context) (image, error) {
	var img image
	tag := imageName(r.config.Environment, r.config.Build.ID)
	out, err := r.command(ctx, "", "image", "ls", "--filter", "reference="+tag, "--format", "{{.ID}}")
	if err != nil {
		return img, err
	}
	if len(out) == 0 {
		return img, nil
	}
	if bytes.Contains(out, []byte("\n")) {
		return img, errors.New("ambiguous image identity")
	}
	out, err = r.command(ctx, "", "image", "inspect", "--format", imageFormat, tag)
	if err != nil {
		return img, err
	}
	if err := json.Unmarshal(out, &img); err != nil {
		return img, fmt.Errorf("decode image observation: %w", err)
	}
	if img.Owner != owner || img.Environment != r.config.Environment || img.InputID != r.config.Build.ID {
		return img, errors.New("refusing image with mismatched ownership or input")
	}
	return img, nil
}

func (r *Runtime) appURL(c container, path string) (string, error) {
	bindings := c.Ports[strconv.Itoa(r.config.Application.Port)+"/tcp"]
	if len(bindings) != 1 || bindings[0].HostIP != "127.0.0.1" || !portPattern.MatchString(bindings[0].HostPort) {
		return "", errors.New("app must have one localhost port binding")
	}
	return "http://127.0.0.1:" + bindings[0].HostPort + path, nil
}

func (r *Runtime) check(ctx context.Context, c container, check goalfile.HTTPCheck) (bool, error) {
	url, err := r.appURL(c, check.Path)
	if err != nil {
		return false, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return false, err
	}
	resp, err := r.client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return false, ctx.Err()
		}
		return false, nil
	}
	defer resp.Body.Close()
	if resp.StatusCode != check.Status {
		return false, nil
	}
	if check.BodyEquals == nil {
		return true, nil
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4097))
	if err != nil {
		return false, err
	}
	return len(body) <= 4096 && string(body) == *check.BodyEquals, nil
}

func checkID(check goalfile.HTTPCheck) string {
	body := "<unset>"
	if check.BodyEquals != nil {
		body = *check.BodyEquals
	}
	sum := sha256.Sum256([]byte(fmt.Sprintf("%q|%d|%t|%q", check.Path, check.Status, check.BodyEquals != nil, body)))
	return hex.EncodeToString(sum[:])
}

func (r *Runtime) snapshot(ctx context.Context) (planning.State, fingerprint, error) {
	var s planning.State
	img, err := r.inspectImage(ctx)
	if err != nil {
		return s, fingerprint{}, err
	}
	s.ImageExists = img.ID != ""
	app, err := r.inspectApp(ctx)
	if err != nil {
		return s, fingerprint{}, err
	}
	s.AppExists = app.ID != ""
	s.AppCurrent = s.AppExists && s.ImageExists && app.ImageID == img.ID && app.Port == strconv.Itoa(r.config.Application.Port)
	fp := fingerprint{app.ID, app.Started, checkID(r.config.Checks.Verification.HTTP)}
	if s.AppCurrent && app.Running {
		s.AppReady, err = r.check(ctx, app, r.config.Checks.Readiness.HTTP)
		if err != nil {
			return s, fp, err
		}
	}
	if previous, ok := r.evidence[r.config.Environment]; ok && s.AppReady && previous == fp {
		s.TestValid = true
	} else {
		delete(r.evidence, r.config.Environment)
	}
	return s, fp, nil
}

func (r *Runtime) Observe(ctx context.Context, environment string) (planning.State, error) {
	if environment != r.config.Environment {
		return planning.State{}, errors.New("wrong environment")
	}
	ctx, cancel := context.WithTimeout(ctx, r.config.OperationTimeout)
	defer cancel()
	s, _, err := r.snapshot(ctx)
	return s, err
}

func pause(ctx context.Context, delay time.Duration) error {
	t := time.NewTimer(delay)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
func (r *Runtime) waitReady(ctx context.Context) error {
	for {
		s, _, err := r.snapshot(ctx)
		if err != nil {
			return err
		}
		if s.AppReady {
			return nil
		}
		if err := pause(ctx, 200*time.Millisecond); err != nil {
			return fmt.Errorf("waiting for readiness: %w", err)
		}
	}
}

func (r *Runtime) Execute(ctx context.Context, environment string, action planning.Action) error {
	if environment != r.config.Environment {
		return errors.New("wrong environment")
	}
	ctx, cancel := context.WithTimeout(ctx, r.config.OperationTimeout)
	defer cancel()
	s, _, err := r.snapshot(ctx)
	if err != nil {
		return err
	}
	switch action.Kind {
	case planning.BuildImage:
		if s.ImageExists {
			return errors.New("image already exists")
		}
		args := []string{"image", "build", "--pull=false", "--file", filepath.Join(r.config.Build.Dir, r.config.Build.Dockerfile), "--tag", imageName(environment, r.config.Build.ID), "--label", labelPrefix + "owner=" + owner, "--label", labelPrefix + "environment=" + environment, "--label", labelPrefix + "input=" + r.config.Build.ID, r.config.Build.Dir}
		_, err = r.command(ctx, "", args...)
		return err
	case planning.DeployApp:
		if !s.ImageExists {
			return errors.New("deployment requires the target image")
		}
		app, err := r.inspectApp(ctx)
		if err != nil {
			return err
		}
		if err := r.remove(ctx, app); err != nil {
			return err
		}
		delete(r.evidence, environment)
		args := append([]string{"run", "-d", "--pull=never", "--name", name(environment), "--publish", fmt.Sprintf("127.0.0.1::%d", r.config.Application.Port)}, labels(environment)...)
		args = append(args, "--label", labelPrefix+"port="+strconv.Itoa(r.config.Application.Port), imageName(environment, r.config.Build.ID))
		if _, err := r.command(ctx, "", args...); err != nil {
			return err
		}
		return r.waitReady(ctx)
	case planning.VerifyApp:
		if !s.AppReady {
			return errors.New("verification requires a ready app")
		}
		app, err := r.inspectApp(ctx)
		if err != nil {
			return err
		}
		passed, err := r.check(ctx, app, r.config.Checks.Verification.HTTP)
		if err != nil {
			return err
		}
		if !passed {
			return errors.New("verification HTTP check failed")
		}
		observed, fp, err := r.snapshot(ctx)
		if err != nil {
			return err
		}
		if !observed.AppReady || fp.ID != app.ID || fp.Started != app.Started {
			return errors.New("app changed during verification")
		}
		r.evidence[environment] = fp
		return nil
	default:
		return fmt.Errorf("unsupported operation %q", action.Kind)
	}
}

func (r *Runtime) remove(ctx context.Context, c container) error {
	if c.ID == "" {
		return nil
	}
	if c.Running {
		if _, err := r.command(ctx, "", "container", "stop", "--time", "10", c.ID); err != nil {
			return err
		}
	}
	_, err := r.command(ctx, "", "container", "rm", c.ID)
	return err
}

func (r *Runtime) Cleanup(ctx context.Context, environment string) error {
	if err := ValidateEnvironment(environment); err != nil {
		return err
	}
	if environment != r.config.Environment {
		return errors.New("wrong environment")
	}
	ctx, cancel := context.WithTimeout(ctx, r.config.OperationTimeout)
	defer cancel()
	app, err := r.inspectApp(ctx)
	if err != nil {
		return err
	}
	if err := r.remove(ctx, app); err != nil {
		return err
	}
	delete(r.evidence, environment)
	return nil
}
