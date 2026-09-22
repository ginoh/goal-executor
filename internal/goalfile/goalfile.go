// Package goalfile reads the single-environment YAML goal format.
package goalfile

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/ginoh/goal-executor/internal/planning"
	"go.yaml.in/yaml/v3"
)

func Load(path string) (planning.Goal, error) {
	f, err := os.Open(path)
	if err != nil {
		return planning.Goal{}, fmt.Errorf("open goal file: %w", err)
	}
	defer f.Close()
	return Decode(f)
}

// Decode requires exactly one mapping with four explicit, typed fields.
// Nodes are checked before conversion to avoid YAML's implicit conversions
// (for example, a number into a Go string or "yes" into a Go bool).
func Decode(r io.Reader) (planning.Goal, error) {
	var goal planning.Goal
	decoder := yaml.NewDecoder(r)
	var document yaml.Node
	if err := decoder.Decode(&document); err != nil {
		return goal, fmt.Errorf("decode goal YAML: %w", err)
	}
	var extra yaml.Node
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err != nil {
			return goal, fmt.Errorf("decode trailing YAML: %w", err)
		}
		return goal, errors.New("goal file must contain exactly one YAML document")
	}
	if len(document.Content) != 1 || document.Content[0].Kind != yaml.MappingNode {
		return goal, errors.New("goal must be a YAML mapping")
	}
	fields := map[string]*yaml.Node{}
	content := document.Content[0].Content
	for i := 0; i < len(content); i += 2 {
		key, value := content[i], content[i+1]
		if key.Kind != yaml.ScalarNode || key.Tag != "!!str" {
			return goal, errors.New("goal field names must be strings")
		}
		name := key.Value
		switch name {
		case "environment", "apiVersion", "dataset", "requireIntegrationTest":
		default:
			return goal, fmt.Errorf("unknown goal field %q", name)
		}
		if _, exists := fields[name]; exists {
			return goal, fmt.Errorf("duplicate goal field %q", name)
		}
		fields[name] = value
	}
	for _, field := range []struct {
		name   string
		target *string
	}{
		{"environment", &goal.Environment}, {"apiVersion", &goal.APIVersion}, {"dataset", &goal.Dataset},
	} {
		n, ok := fields[field.name]
		if !ok {
			return planning.Goal{}, fmt.Errorf("missing goal field %q", field.name)
		}
		if n.Kind != yaml.ScalarNode || n.Tag != "!!str" || strings.TrimSpace(n.Value) == "" {
			return planning.Goal{}, fmt.Errorf("goal field %q must be a non-empty string", field.name)
		}
		*field.target = n.Value
	}
	n, ok := fields["requireIntegrationTest"]
	if !ok {
		return planning.Goal{}, errors.New("missing goal field \"requireIntegrationTest\"")
	}
	if n.Kind != yaml.ScalarNode || n.Tag != "!!bool" {
		return planning.Goal{}, errors.New("requireIntegrationTest must be a boolean")
	}
	if err := n.Decode(&goal.RequireIntegrationTest); err != nil {
		return planning.Goal{}, fmt.Errorf("decode requireIntegrationTest: %w", err)
	}
	return goal, nil
}
