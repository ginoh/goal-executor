// Package planning derives sequential plans without accessing external resources.
package planning

import (
	"fmt"
	"strings"
)

type Goal struct {
	Environment            string
	APIVersion             string
	Dataset                string
	RequireIntegrationTest bool
}

type DBState struct {
	Exists  bool
	Ready   bool
	Dataset string // Empty means uninitialized, not unknown.
}

type APIState struct {
	Exists  bool
	Ready   bool
	Version string
}

// State is a comparable projection of one environment's observed state.
// Observation errors must be handled before calling Plan, not encoded as absence.
type State struct {
	DB  DBState
	API APIState
	// TestValid means a successful test matches this exact observed configuration.
	// The observer must check resource identities and data generations before
	// setting it. The planner does not establish the validity of real evidence.
	TestValid bool
}

type ActionKind string

const (
	CreateDB           ActionKind = "create-db"
	InitializeData     ActionKind = "initialize-data"
	DeployAPI          ActionKind = "deploy-api"
	RunIntegrationTest ActionKind = "run-integration-test"
)

// Action describes an operation, not an executable function.
type Action struct {
	Kind       ActionKind
	APIVersion string
	Dataset    string
	Reason     string
}

type Status string

const (
	Achieved     Status = "achieved"
	Planned      Status = "planned"
	Unreachable  Status = "unreachable"
	LimitReached Status = "limit-reached"
)

type Result struct {
	Status  Status
	Actions []Action
	Reason  string
}

const DefaultMaxStates = 1000

type Options struct {
	// MaxStates bounds distinct discovered states, including the initial state.
	// Zero selects DefaultMaxStates. Negative values are invalid.
	MaxStates int
}

func (g Goal) resourcesReady(s State) bool {
	return s.DB.Exists && s.DB.Ready && s.DB.Dataset == g.Dataset &&
		s.API.Exists && s.API.Ready && s.API.Version == g.APIVersion
}

func (g Goal) satisfied(s State) bool {
	return g.resourcesReady(s) && (!g.RequireIntegrationTest || s.TestValid)
}

func (g Goal) Validate() error {
	if strings.TrimSpace(g.Environment) == "" || strings.TrimSpace(g.APIVersion) == "" || strings.TrimSpace(g.Dataset) == "" {
		return fmt.Errorf("environment, API version, and dataset must be non-empty")
	}
	return nil
}

func validate(g Goal, s State, options Options) error {
	if err := g.Validate(); err != nil {
		return err
	}
	if options.MaxStates < 0 {
		return fmt.Errorf("max states must not be negative")
	}
	if !s.DB.Exists && (s.DB.Ready || s.DB.Dataset != "") {
		return fmt.Errorf("absent DB cannot be ready or contain a dataset")
	}
	if !s.API.Exists && (s.API.Ready || s.API.Version != "") {
		return fmt.Errorf("absent API cannot be ready or have a version")
	}
	if s.API.Ready && strings.TrimSpace(s.API.Version) == "" {
		return fmt.Errorf("ready API must have a version")
	}
	if s.TestValid && (!s.DB.Exists || !s.DB.Ready || s.DB.Dataset == "" || !s.API.Exists || !s.API.Ready) {
		return fmt.Errorf("valid test evidence requires a ready API and initialized DB")
	}
	return nil
}
