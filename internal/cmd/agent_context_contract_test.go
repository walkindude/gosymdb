package cmd

import (
	"strings"
	"testing"
)

// TestAgentContextMatchesCobra is a drift guard between the ordered list of
// commands printed by `gosymdb agent-context` and the Cobra command surface.
//
// agent-context is the "teach the agent the API" command. If it falls out of
// sync with Cobra, agents silently lose access to capabilities (command
// registered in Cobra but missing from agent-context) or waste tokens
// describing commands that no longer exist (the reverse).
//
// Invariant: agentContextOrder ∪ excludedFromAgentContext = set of Cobra
// subcommands registered on rootCmd. The two sets are disjoint.
func TestAgentContextMatchesCobra(t *testing.T) {
	declared := make(map[string]bool, len(agentContextOrder))
	for _, key := range agentContextOrder {
		name := strings.TrimPrefix(key, "gosymdb ")
		if name == key {
			t.Errorf("agentContextOrder entry %q does not start with \"gosymdb \" — invariant broken", key)
		}
		if declared[name] {
			t.Errorf("agentContextOrder lists %q twice", name)
		}
		declared[name] = true
	}

	registered := make(map[string]bool)
	for _, c := range rootCmd.Commands() {
		registered[c.Name()] = true
	}

	// Direction 1: every registered Cobra subcommand is either in
	// agentContextOrder or explicitly excluded.
	for name := range registered {
		_, excluded := excludedFromAgentContext[name]
		switch {
		case declared[name] && excluded:
			t.Errorf("command %q is both in agentContextOrder and excludedFromAgentContext — pick one", name)
		case declared[name]:
			// OK
		case excluded:
			// OK
		default:
			t.Errorf("Cobra registers command %q that is missing from agent-context "+
				"(add it to agentContextOrder or mark it excluded in excludedFromAgentContext)", name)
		}
	}

	// Direction 2: every entry in agentContextOrder maps to a real Cobra command.
	for name := range declared {
		if !registered[name] {
			t.Errorf("agentContextOrder declares %q but no such Cobra command is registered", name)
		}
	}

	// Direction 3: every excluded name maps to a real Cobra command (catches
	// dead entries in the exclusion map after a command is removed). Cobra
	// built-ins `help` and `completion` are allowed to be absent in unusual
	// test configurations but are almost always present; skip them.
	for name := range excludedFromAgentContext {
		if name == "help" || name == "completion" {
			continue
		}
		if !registered[name] {
			t.Errorf("excludedFromAgentContext lists %q but no such Cobra command is registered — stale exclusion?", name)
		}
	}

	// Direction 4: every entry in agentContextOrder has a matching helpSpecs
	// entry, otherwise execAgentContext silently drops it.
	for _, key := range agentContextOrder {
		if _, ok := helpSpecs[key]; !ok {
			t.Errorf("agentContextOrder declares %q but helpSpecs has no matching entry", key)
		}
	}
}
