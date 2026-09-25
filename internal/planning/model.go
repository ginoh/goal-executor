// Package planning derives sequential plans without accessing external resources.
package planning

import (
	"errors"
	"strings"
)

type Goal struct {
	Environment string
	InputID     string
	State       string // ready or verified
}

// State is a comparable projection of one environment's observed state.
// Observation errors must be returned, not encoded as absence.
type State struct {
	ImageExists bool
	AppExists   bool
	AppCurrent  bool
	AppReady    bool
	TestValid   bool
}

type ActionKind string

const (
	BuildImage ActionKind = "build-image"
	DeployApp  ActionKind = "deploy-app"
	VerifyApp  ActionKind = "verify-app"
)

type Action struct {
	Kind   ActionKind
	Reason string
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

type Options struct{ MaxStates int }

func (g Goal) satisfied(s State) bool {
	return s.ImageExists && s.AppCurrent && s.AppReady && (g.State == "ready" || s.TestValid)
}

func (g Goal) Validate() error {
	if strings.TrimSpace(g.Environment) == "" || strings.TrimSpace(g.InputID) == "" || (g.State != "ready" && g.State != "verified") {
		return errors.New("environment, input ID, and state ready or verified are required")
	}
	return nil
}

func validate(g Goal, s State, options Options) error {
	if err := g.Validate(); err != nil {
		return err
	}
	if options.MaxStates < 0 {
		return errors.New("max states must not be negative")
	}
	if s.AppCurrent && (!s.AppExists || !s.ImageExists) {
		return errors.New("current app requires an existing app and image")
	}
	if s.AppReady && !s.AppCurrent {
		return errors.New("ready app must be current")
	}
	if s.TestValid && !s.AppReady {
		return errors.New("valid test requires a ready app")
	}
	return nil
}
