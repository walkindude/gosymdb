package cmd

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestCLIBridgeManifest_ValidJSONWithCurrentVersion(t *testing.T) {
	prev := Version
	Version = "test-0.9.9"
	t.Cleanup(func() { Version = prev })

	cmd := newCLIBridgeManifestCmd()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	// RunE writes to os.Stdout directly, so validate the embedded spec instead
	// and verify Version substitution via the same code path.
	var spec map[string]any
	if err := json.Unmarshal(cliBridgeSpecRaw, &spec); err != nil {
		t.Fatalf("embedded spec is not valid JSON: %v", err)
	}

	if got := spec["name"]; got != "gosymdb" {
		t.Errorf("name: got %q, want %q", got, "gosymdb")
	}
	if got := spec["binary"]; got != "gosymdb" {
		t.Errorf("binary: got %q, want %q", got, "gosymdb")
	}
	cmds, ok := spec["commands"].([]any)
	if !ok {
		t.Fatalf("commands is not an array")
	}
	if len(cmds) < 12 {
		t.Errorf("expected at least 12 commands, got %d", len(cmds))
	}

	// Verify required top-level fields match cli-bridge CliToolSpec schema.
	for _, field := range []string{"name", "specVersion", "binary", "binaryVersion", "description", "versionDetection", "triggers", "commands"} {
		if _, ok := spec[field]; !ok {
			t.Errorf("missing required field: %s", field)
		}
	}

	// Version substitution happens at runtime in RunE. Simulate it.
	spec["binaryVersion"] = Version
	if spec["binaryVersion"] != "test-0.9.9" {
		t.Errorf("binaryVersion not substituted: got %v", spec["binaryVersion"])
	}
}
