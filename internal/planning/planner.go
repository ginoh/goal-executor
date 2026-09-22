package planning

import "fmt"

// Plan finds a shortest plan by operation count. The caller must supply state
// from goal.Environment. It never executes actions or modifies the input state.
func Plan(goal Goal, state State, options Options) (Result, error) {
	if err := validate(goal, state, options); err != nil {
		return Result{}, err
	}
	maxStates := options.MaxStates
	if maxStates == 0 {
		maxStates = DefaultMaxStates
	}
	return search(state, goal.satisfied, operations(goal), maxStates), nil
}

type node struct {
	state  State
	parent int
	action Action
}

func search(initial State, satisfied func(State) bool, ops []operation, maxStates int) Result {
	queue := []node{{state: initial, parent: -1}}
	visited := map[State]bool{initial: true}
	for head := 0; head < len(queue); head++ {
		current := queue[head]
		if satisfied(current.state) {
			if head == 0 {
				return Result{Status: Achieved, Reason: "observed state satisfies the goal"}
			}
			actions := path(queue, head)
			return Result{Status: Planned, Actions: actions,
				Reason: fmt.Sprintf("goal is reachable in %d operations under the prediction model", len(actions))}
		}
		for _, op := range ops {
			if !op.applicable(current.state) {
				continue
			}
			next := op.predict(current.state)
			if visited[next] {
				continue
			}
			if len(visited) >= maxStates {
				return Result{Status: LimitReached,
					Reason: fmt.Sprintf("search requires more than %d distinct states; reachability is unknown", maxStates)}
			}
			visited[next] = true
			queue = append(queue, node{state: next, parent: head, action: op.action})
		}
	}
	return Result{Status: Unreachable, Reason: "all reachable states were explored; defined operations cannot satisfy the goal"}
}

func path(queue []node, index int) []Action {
	var reversed []Action
	for queue[index].parent != -1 {
		reversed = append(reversed, queue[index].action)
		index = queue[index].parent
	}
	actions := make([]Action, len(reversed))
	for i := range reversed {
		actions[len(reversed)-1-i] = reversed[i]
	}
	return actions
}
