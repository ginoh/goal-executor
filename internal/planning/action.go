package planning

type operation struct {
	action     Action
	applicable func(State) bool
	predict    func(State) State
}

func operations(g Goal) []operation {
	return []operation{
		{Action{BuildImage, "image for the fixed build input is absent"}, func(s State) bool { return !s.ImageExists }, func(s State) State { s.ImageExists = true; return s }},
		{Action{DeployApp, "app is absent, outdated, or unready"}, func(s State) bool { return s.ImageExists && (!s.AppCurrent || !s.AppReady) }, func(s State) State {
			s.AppExists, s.AppCurrent, s.AppReady, s.TestValid = true, true, true, false
			return s
		}},
		{Action{VerifyApp, "ready app has no valid verification"}, func(s State) bool { return g.State == "verified" && s.AppReady && !s.TestValid }, func(s State) State { s.TestValid = true; return s }},
	}
}
