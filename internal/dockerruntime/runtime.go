// Package dockerruntime implements the single-environment PoC with Docker CLI.
package dockerruntime

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"github.com/ginoh/goal-executor/internal/planning"
)

const owner = "goal-executor-cli-v1"
const labelPrefix = "io.goal-executor."
const Dataset = "sample-v1"
const SampleValue = "hello from postgres"

var environmentPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,39}$`)

func ValidateEnvironment(environment string) error {
	if !environmentPattern.MatchString(environment) {
		return errors.New("environment must match [a-z][a-z0-9-]{0,39}")
	}
	return nil
}

type Config struct {
	DBImage          string
	APIImage         string
	OperationTimeout time.Duration
	DeployDelay      time.Duration // Optional demonstration delay, bounded by operation timeout.
}

type command func(context.Context, string, ...string) ([]byte, error)

type Runtime struct {
	config   Config
	command  command
	client   *http.Client
	evidence map[string]fingerprint
}

func New(config Config) (*Runtime, error) {
	if config.DBImage == "" || config.APIImage == "" {
		return nil, errors.New("DB and API images are required")
	}
	if config.OperationTimeout <= 0 || config.DeployDelay < 0 || config.DeployDelay >= config.OperationTimeout {
		return nil, errors.New("timeout must be positive and deployment delay must be non-negative and less than timeout")
	}
	return &Runtime{config: config, command: dockerCommand,
		client: &http.Client{Timeout: 3 * time.Second}, evidence: make(map[string]fingerprint)}, nil
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
		// Do not include arbitrary daemon/container output in error reports.
		return nil, fmt.Errorf("docker %s failed: %w", args[0], err)
	}
	return bytes.TrimSpace(out), nil
}

type container struct {
	ID          string `json:"id"`
	Running     bool   `json:"running"`
	Started     string `json:"started"`
	Owner       string `json:"owner"`
	Environment string `json:"environment"`
	Role        string `json:"role"`
	Ports       map[string][]struct {
		HostIP   string
		HostPort string
	} `json:"ports"`
}

const containerFormat = `{"id":{{json .Id}},"running":{{json .State.Running}},"started":{{json .State.StartedAt}},"owner":{{json (index .Config.Labels "io.goal-executor.owner")}},"environment":{{json (index .Config.Labels "io.goal-executor.environment")}},"role":{{json (index .Config.Labels "io.goal-executor.role")}},"ports":{{json .NetworkSettings.Ports}}}`

func name(environment, role string) string { return "goal-executor-" + environment + "-" + role }

func labels(environment, role string) []string {
	return []string{"--label", labelPrefix + "owner=" + owner, "--label", labelPrefix + "environment=" + environment, "--label", labelPrefix + "role=" + role}
}

func (r *Runtime) inspect(ctx context.Context, environment, role string) (container, error) {
	var c container
	out, err := r.command(ctx, "", "container", "ls", "-a", "--filter", "name=^/"+name(environment, role)+"$", "--format", "{{.ID}}")
	if err != nil {
		return c, err
	}
	if len(out) == 0 {
		return c, nil
	}
	if strings.Contains(string(out), "\n") {
		return c, errors.New("ambiguous container identity")
	}
	out, err = r.command(ctx, "", "container", "inspect", "--format", containerFormat, string(out))
	if err != nil {
		return c, err
	}
	if err = json.Unmarshal(out, &c); err != nil {
		return c, fmt.Errorf("decode container observation: %w", err)
	}
	if c.Owner != owner || c.Environment != environment || c.Role != role {
		return c, fmt.Errorf("refusing unowned container %s", name(environment, role))
	}
	return c, nil
}

type Data struct {
	Version    string `json:"version"`
	Dataset    string `json:"dataset"`
	Generation string `json:"generation"`
	Value      string `json:"value"`
}

type fingerprint struct {
	DBID, DBStarted, APIID, APIStarted string
	Data                               Data
}

func (r *Runtime) sql(ctx context.Context, id, sql string) ([]byte, error) {
	return r.command(ctx, sql, "exec", "-i", id, "psql", "-X", "-U", "postgres", "-d", "postgres", "-v", "ON_ERROR_STOP=1", "-Atq")
}

const readData = `SELECT json_build_object('dataset',dataset,'generation',generation,'value',value)::text FROM goal_seed WHERE id=1;`

func (r *Runtime) snapshot(ctx context.Context, environment string) (planning.State, fingerprint, error) {
	var s planning.State
	var fp fingerprint
	if err := ValidateEnvironment(environment); err != nil {
		return s, fp, err
	}
	db, err := r.inspect(ctx, environment, "db")
	if err != nil {
		return s, fp, err
	}
	api, err := r.inspect(ctx, environment, "api")
	if err != nil {
		return s, fp, err
	}
	s.DB.Exists = db.ID != ""
	s.API.Exists = api.ID != ""
	fp.DBID = db.ID
	fp.DBStarted = db.Started
	fp.APIID = api.ID
	fp.APIStarted = api.Started
	var data Data
	if db.Running {
		_, err = r.command(ctx, "", "exec", db.ID, "pg_isready", "-h", "127.0.0.1", "-U", "postgres", "-d", "postgres")
		if err != nil {
			var exit *exec.ExitError
			if !errors.As(err, &exit) || (exit.ExitCode() != 1 && exit.ExitCode() != 2) {
				return s, fp, err
			}
		} else {
			s.DB.Ready = true
			out, err := r.sql(ctx, db.ID, `SELECT to_regclass('public.goal_seed') IS NOT NULL;`)
			if err != nil {
				return s, fp, err
			}
			if string(out) == "t" {
				out, err = r.sql(ctx, db.ID, readData)
				if err != nil {
					return s, fp, err
				}
				if err = json.Unmarshal(out, &data); err != nil {
					return s, fp, fmt.Errorf("invalid DB seed observation: %w", err)
				}
				if data.Dataset == "" || data.Generation == "" {
					return s, fp, errors.New("incomplete DB seed metadata")
				}
				s.DB.Dataset = data.Dataset
			}
		}
	}
	if api.Running {
		if _, err := endpoint(api); err != nil {
			return s, fp, err
		}
		actual, err := r.getData(ctx, api)
		if err != nil {
			if ctx.Err() != nil {
				return s, fp, ctx.Err()
			}
			// HTTP readiness failure is observed as unready, not as absence.
		} else {
			s.API.Version = actual.Version
			s.API.Ready = s.DB.Ready && actual.Version != "" && data.Generation != "" && actual.Dataset == data.Dataset && actual.Generation == data.Generation && actual.Value == data.Value
			fp.Data = actual
		}
	}
	if previous, ok := r.evidence[environment]; ok && s.DB.Ready && s.API.Ready && previous == fp {
		s.TestValid = true
	} else {
		delete(r.evidence, environment)
	}
	return s, fp, nil
}

func (r *Runtime) Observe(ctx context.Context, environment string) (planning.State, error) {
	ctx, cancel := context.WithTimeout(ctx, r.config.OperationTimeout)
	defer cancel()
	s, _, err := r.snapshot(ctx, environment)
	return s, err
}

func endpoint(c container) (string, error) {
	bindings := c.Ports["8080/tcp"]
	if len(bindings) != 1 || bindings[0].HostIP != "127.0.0.1" || !regexp.MustCompile(`^[0-9]+$`).MatchString(bindings[0].HostPort) {
		return "", errors.New("API must have one localhost port binding")
	}
	return "http://127.0.0.1:" + bindings[0].HostPort + "/data", nil
}

func (r *Runtime) getData(ctx context.Context, api container) (Data, error) {
	var data Data
	url, err := endpoint(api)
	if err != nil {
		return data, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return data, err
	}
	resp, err := r.client.Do(req)
	if err != nil {
		return data, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return data, fmt.Errorf("API returned HTTP %d", resp.StatusCode)
	}
	err = json.NewDecoder(io.LimitReader(resp.Body, 4096)).Decode(&data)
	return data, err
}

func pause(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func (r *Runtime) waitReady(ctx context.Context, environment string, api bool) error {
	for {
		s, _, err := r.snapshot(ctx, environment)
		if err != nil {
			return err
		}
		if (!api && s.DB.Ready) || (api && s.API.Ready) {
			return nil
		}
		if err := pause(ctx, 200*time.Millisecond); err != nil {
			return fmt.Errorf("waiting for readiness: %w", err)
		}
	}
}

func (r *Runtime) network(ctx context.Context, environment string, create bool) (string, error) {
	n := name(environment, "net")
	out, err := r.command(ctx, "", "network", "ls", "--filter", "name=^"+n+"$", "--format", "{{.ID}}")
	if err != nil {
		return "", err
	}
	if len(out) == 0 {
		if !create {
			return "", nil
		}
		args := append([]string{"network", "create"}, labels(environment, "net")...)
		out, err = r.command(ctx, "", append(args, n)...)
		return string(out), err
	}
	if strings.Contains(string(out), "\n") {
		return "", errors.New("ambiguous network identity")
	}
	id := string(out)
	out, err = r.command(ctx, "", "network", "inspect", "--format", `{{index .Labels "io.goal-executor.owner"}}|{{index .Labels "io.goal-executor.environment"}}|{{index .Labels "io.goal-executor.role"}}`, id)
	if err != nil {
		return "", err
	}
	if string(out) != owner+"|"+environment+"|net" {
		return "", errors.New("refusing unowned network")
	}
	return id, nil
}

func (r *Runtime) Execute(ctx context.Context, environment string, action planning.Action) error {
	if err := ValidateEnvironment(environment); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, r.config.OperationTimeout)
	defer cancel()
	delete(r.evidence, environment)
	s, _, err := r.snapshot(ctx, environment)
	if err != nil {
		return err
	}
	switch action.Kind {
	case planning.CreateDB:
		if s.DB.Exists {
			return errors.New("DB already exists")
		}
		if _, err := r.network(ctx, environment, true); err != nil {
			return err
		}
		args := append([]string{"run", "-d", "--pull=never", "--name", name(environment, "db"), "--network", name(environment, "net"), "--network-alias", "db", "--tmpfs", "/var/lib/postgresql/data:rw", "-e", "POSTGRES_HOST_AUTH_METHOD=trust"}, labels(environment, "db")...)
		if _, err := r.command(ctx, "", append(args, r.config.DBImage)...); err != nil {
			return err
		}
		return r.waitReady(ctx, environment, false)
	case planning.InitializeData:
		if !s.DB.Ready || s.DB.Dataset != "" || action.Dataset != Dataset {
			return errors.New("initialization requires an uninitialized ready DB and dataset sample-v1")
		}
		db, err := r.inspect(ctx, environment, "db")
		if err != nil {
			return err
		}
		_, err = r.sql(ctx, db.ID, `BEGIN; CREATE TABLE goal_seed (id integer PRIMARY KEY CHECK (id=1), dataset text NOT NULL, generation text NOT NULL, value text NOT NULL); INSERT INTO goal_seed VALUES (1,'sample-v1',gen_random_uuid()::text,'hello from postgres'); COMMIT;`)
		return err
	case planning.DeployAPI:
		if !s.DB.Ready || s.DB.Dataset != Dataset || (action.APIVersion != "v1" && action.APIVersion != "v2") {
			return errors.New("deployment requires sample-v1 DB and API version v1 or v2")
		}
		if err := pause(ctx, r.config.DeployDelay); err != nil {
			return err
		}
		api, err := r.inspect(ctx, environment, "api")
		if err != nil {
			return err
		}
		if err := r.remove(ctx, api); err != nil {
			return err
		}
		args := append([]string{"run", "-d", "--pull=never", "--name", name(environment, "api"), "--network", name(environment, "net"), "--tmpfs", "/var/lib/postgresql/data:rw", "-p", "127.0.0.1::8080", "--entrypoint", "/app/demo-api", "-e", "API_VERSION=" + action.APIVersion, "-e", "DB_HOST=db"}, labels(environment, "api")...)
		if _, err := r.command(ctx, "", append(args, r.config.APIImage)...); err != nil {
			return err
		}
		return r.waitReady(ctx, environment, true)
	case planning.RunIntegrationTest:
		observed, fp, err := r.snapshot(ctx, environment)
		if err != nil {
			return err
		}
		if !observed.API.Ready || !observed.DB.Ready || fp.Data.Version != action.APIVersion || fp.Data.Dataset != action.Dataset || fp.Data.Value != SampleValue || fp.Data.Generation == "" {
			return errors.New("integration test failed: API version or DB data does not match the goal")
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
	ctx, cancel := context.WithTimeout(ctx, r.config.OperationTimeout)
	defer cancel()
	// Validate all ownership before removing anything.
	api, err := r.inspect(ctx, environment, "api")
	if err != nil {
		return err
	}
	db, err := r.inspect(ctx, environment, "db")
	if err != nil {
		return err
	}
	net, err := r.network(ctx, environment, false)
	if err != nil {
		return err
	}
	if err = r.remove(ctx, api); err != nil {
		return err
	}
	if err = r.remove(ctx, db); err != nil {
		return err
	}
	if net != "" {
		if _, err = r.command(ctx, "", "network", "rm", net); err != nil {
			return err
		}
	}
	delete(r.evidence, environment)
	return nil
}
