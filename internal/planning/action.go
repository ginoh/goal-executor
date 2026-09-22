package planning

import "fmt"

// operation contains planning knowledge only. Predictors return a new value;
// none of these functions performs an operation or changes an observation.
type operation struct {
	action     Action
	applicable func(State) bool
	predict    func(State) State
}

func operations(g Goal) []operation {
	return []operation{
		{
			action:     Action{Kind: CreateDB, Reason: "DB is absent"},
			applicable: func(s State) bool { return !s.DB.Exists },
			predict: func(s State) State {
				s.DB = DBState{Exists: true, Ready: true}
				s.TestValid = false
				return s
			},
		},
		{
			action: Action{Kind: InitializeData, Dataset: g.Dataset,
				Reason: fmt.Sprintf("DB is ready but uninitialized; dataset %q is required", g.Dataset)},
			applicable: func(s State) bool {
				return s.DB.Exists && s.DB.Ready && s.DB.Dataset == ""
			},
			predict: func(s State) State {
				s.DB.Dataset = g.Dataset
				s.TestValid = false
				return s
			},
		},
		{
			action: Action{Kind: DeployAPI, APIVersion: g.APIVersion,
				Reason: fmt.Sprintf("required DB dataset is ready; API %q is not ready", g.APIVersion)},
			applicable: func(s State) bool {
				return s.DB.Exists && s.DB.Ready && s.DB.Dataset == g.Dataset &&
					(!s.API.Exists || !s.API.Ready || s.API.Version != g.APIVersion)
			},
			predict: func(s State) State {
				s.API = APIState{Exists: true, Ready: true, Version: g.APIVersion}
				s.TestValid = false
				return s
			},
		},
		{
			action: Action{Kind: RunIntegrationTest, APIVersion: g.APIVersion, Dataset: g.Dataset,
				Reason: "target resources are ready but have no valid successful integration test"},
			applicable: func(s State) bool {
				return g.RequireIntegrationTest && g.resourcesReady(s) && !s.TestValid
			},
			predict: func(s State) State {
				s.TestValid = true
				return s
			},
		},
	}
}
