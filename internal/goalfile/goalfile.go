// Package goalfile reads the application configuration and desired goal.
package goalfile

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"strings"

	"go.yaml.in/yaml/v3"
)

type Document struct {
	Environment string      `yaml:"environment"`
	Application Application `yaml:"application"`
	Checks      Checks      `yaml:"checks"`
	Goal        Goal        `yaml:"goal"`
}
type Application struct {
	Build Build `yaml:"build"`
	Port  int   `yaml:"port"`
}
type Build struct {
	Context    string `yaml:"context"`
	Dockerfile string `yaml:"dockerfile"`
}
type Checks struct {
	Readiness    Check `yaml:"readiness"`
	Verification Check `yaml:"verification"`
}
type Check struct {
	HTTP HTTPCheck `yaml:"http"`
}
type HTTPCheck struct {
	Path       string  `yaml:"path"`
	Status     int     `yaml:"status"`
	BodyEquals *string `yaml:"bodyEquals"`
}
type Goal struct {
	State string `yaml:"state"`
}

func Load(file string) (Document, error) {
	f, err := os.Open(file)
	if err != nil {
		return Document{}, fmt.Errorf("open goal file: %w", err)
	}
	defer f.Close()
	return Decode(f)
}

func Decode(r io.Reader) (Document, error) {
	var document yaml.Node
	decoder := yaml.NewDecoder(r)
	if err := decoder.Decode(&document); err != nil {
		return Document{}, fmt.Errorf("decode goal YAML: %w", err)
	}
	var extra yaml.Node
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err != nil {
			return Document{}, fmt.Errorf("decode trailing YAML: %w", err)
		}
		return Document{}, errors.New("goal file must contain exactly one YAML document")
	}
	if len(document.Content) != 1 {
		return Document{}, errors.New("goal must be a YAML mapping")
	}
	if err := validateNodes(document.Content[0]); err != nil {
		return Document{}, err
	}
	var d Document
	if err := document.Decode(&d); err != nil {
		return d, fmt.Errorf("decode configuration: %w", err)
	}
	if err := d.Validate(); err != nil {
		return Document{}, err
	}
	return d, nil
}

var fields = map[string]map[string]string{
	"":                         {"environment": "!!str", "application": "map", "checks": "map", "goal": "map"},
	"application":              {"build": "map", "port": "!!int"},
	"application.build":        {"context": "!!str", "dockerfile": "!!str"},
	"checks":                   {"readiness": "map", "verification": "map"},
	"checks.readiness":         {"http": "map"},
	"checks.verification":      {"http": "map"},
	"checks.readiness.http":    {"path": "!!str", "status": "!!int", "bodyEquals": "!!str"},
	"checks.verification.http": {"path": "!!str", "status": "!!int", "bodyEquals": "!!str"},
	"goal":                     {"state": "!!str"},
}

func validateNodes(n *yaml.Node) error { return validateMapping(n, "") }
func validateMapping(n *yaml.Node, location string) error {
	if n.Kind != yaml.MappingNode {
		return fmt.Errorf("%s must be a mapping", location)
	}
	seen := map[string]bool{}
	for i := 0; i < len(n.Content); i += 2 {
		key, value := n.Content[i], n.Content[i+1]
		if key.Kind != yaml.ScalarNode || key.Tag != "!!str" {
			return errors.New("field names must be strings")
		}
		name := key.Value
		want, ok := fields[location][name]
		if !ok {
			return fmt.Errorf("unknown field %s", path.Join(location, name))
		}
		if seen[name] {
			return fmt.Errorf("duplicate field %s", path.Join(location, name))
		}
		seen[name] = true
		if want == "map" {
			loc := name
			if location != "" {
				loc = location + "." + name
			}
			if err := validateMapping(value, loc); err != nil {
				return err
			}
		} else if value.Kind != yaml.ScalarNode || value.Tag != want {
			return fmt.Errorf("field %s must be %s", path.Join(location, name), want)
		}
	}
	return nil
}

func (d Document) Validate() error {
	if strings.TrimSpace(d.Environment) == "" || strings.TrimSpace(d.Application.Build.Context) == "" || strings.TrimSpace(d.Application.Build.Dockerfile) == "" {
		return errors.New("environment, build context, and Dockerfile are required")
	}
	if d.Application.Port < 1 || d.Application.Port > 65535 {
		return errors.New("application port must be between 1 and 65535")
	}
	for _, c := range []struct {
		name  string
		value HTTPCheck
	}{{"readiness", d.Checks.Readiness.HTTP}, {"verification", d.Checks.Verification.HTTP}} {
		if c.name == "verification" && c.value.Path == "" && c.value.Status == 0 && c.value.BodyEquals == nil {
			if d.Goal.State == "ready" {
				continue
			}
			return errors.New("verified goal requires a verification check")
		}
		if !strings.HasPrefix(c.value.Path, "/") || strings.HasPrefix(c.value.Path, "//") || strings.ContainsAny(c.value.Path, "?#") || c.value.Status < 100 || c.value.Status > 599 {
			return fmt.Errorf("invalid %s HTTP check", c.name)
		}
	}
	if d.Goal.State != "ready" && d.Goal.State != "verified" {
		return errors.New("goal.state must be ready or verified")
	}
	return nil
}
