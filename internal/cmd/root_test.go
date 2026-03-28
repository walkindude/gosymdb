package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// TestDiscoverDBFindsInParent verifies that discoverDB walks up the directory
// tree and returns the path of a gosymdb.sqlite found in a parent directory.
func TestDiscoverDBFindsInParent(t *testing.T) {
	// Create temp structure: <tmp>/gosymdb.sqlite and <tmp>/sub/
	tmp := t.TempDir()
	dbFile := filepath.Join(tmp, "gosymdb.sqlite")
	if err := os.WriteFile(dbFile, []byte(""), 0644); err != nil {
		t.Fatalf("create dummy db: %v", err)
	}
	subDir := filepath.Join(tmp, "sub")
	if err := os.MkdirAll(subDir, 0755); err != nil {
		t.Fatalf("mkdir sub: %v", err)
	}

	got := discoverDB(subDir, "gosymdb.sqlite")
	if got != dbFile {
		t.Errorf("discoverDB(%q, %q) = %q; want %q", subDir, "gosymdb.sqlite", got, dbFile)
	}
}

// TestDiscoverDBReturnsDefaultWhenNotFound verifies that discoverDB returns the
// original default path when no gosymdb.sqlite is found in any parent.
func TestDiscoverDBReturnsDefaultWhenNotFound(t *testing.T) {
	// Create a temp dir with NO gosymdb.sqlite anywhere.
	tmp := t.TempDir()
	subDir := filepath.Join(tmp, "a", "b", "c")
	if err := os.MkdirAll(subDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	defaultDB := "gosymdb.sqlite"
	got := discoverDB(subDir, defaultDB)
	if got != defaultDB {
		t.Errorf("discoverDB(%q, %q) = %q; want %q (unchanged)", subDir, defaultDB, got, defaultDB)
	}
}

// TestDiscoverDBUsesExistingPathDirectly verifies that if the given path already
// points to an existing file, discoverDB returns it unchanged (no walk needed).
func TestDiscoverDBUsesExistingPathDirectly(t *testing.T) {
	tmp := t.TempDir()
	dbFile := filepath.Join(tmp, "mydb.sqlite")
	if err := os.WriteFile(dbFile, []byte(""), 0644); err != nil {
		t.Fatalf("create db: %v", err)
	}

	// If the provided path exists, discoverDB should return it immediately.
	got := discoverDB(tmp, dbFile)
	if got != dbFile {
		t.Errorf("discoverDB(%q, %q) = %q; want %q", tmp, dbFile, got, dbFile)
	}
}

// TestDiscoverDBDeepNesting verifies walk works through multiple levels.
func TestDiscoverDBDeepNesting(t *testing.T) {
	tmp := t.TempDir()
	dbFile := filepath.Join(tmp, "gosymdb.sqlite")
	if err := os.WriteFile(dbFile, []byte(""), 0644); err != nil {
		t.Fatalf("create dummy db: %v", err)
	}
	deepDir := filepath.Join(tmp, "a", "b", "c", "d")
	if err := os.MkdirAll(deepDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	got := discoverDB(deepDir, "gosymdb.sqlite")
	if got != dbFile {
		t.Errorf("discoverDB(%q, %q) = %q; want %q", deepDir, "gosymdb.sqlite", got, dbFile)
	}
}

// — isDBExemptCmd ——————————————————————————————————————————————————————————

// TestIsDBExemptCmdRootIsExempt ensures the root command (no parent) is exempt
// from the no-DB-found error so that `gosymdb` with no args shows help rather
// than failing with "no database found".
func TestIsDBExemptCmdRootIsExempt(t *testing.T) {
	root := &cobra.Command{Use: "gosymdb"}
	if !isDBExemptCmd(root) {
		t.Error("root command (no parent) must be exempt from no-DB check")
	}
}

func TestIsDBExemptCmdIndexIsExempt(t *testing.T) {
	root := &cobra.Command{Use: "gosymdb"}
	sub := &cobra.Command{Use: "index"}
	root.AddCommand(sub)
	if !isDBExemptCmd(sub) {
		t.Error("'index' subcommand must be exempt")
	}
}

func TestIsDBExemptCmdVersionIsExempt(t *testing.T) {
	root := &cobra.Command{Use: "gosymdb"}
	sub := &cobra.Command{Use: "version"}
	root.AddCommand(sub)
	if !isDBExemptCmd(sub) {
		t.Error("'version' subcommand must be exempt")
	}
}

func TestIsDBExemptCmdAgentContextIsExempt(t *testing.T) {
	root := &cobra.Command{Use: "gosymdb"}
	sub := &cobra.Command{Use: "agent-context"}
	root.AddCommand(sub)
	if !isDBExemptCmd(sub) {
		t.Error("'agent-context' subcommand must be exempt")
	}
}

func TestIsDBExemptCmdHelpIsExempt(t *testing.T) {
	root := &cobra.Command{Use: "gosymdb"}
	sub := &cobra.Command{Use: "help"}
	root.AddCommand(sub)
	if !isDBExemptCmd(sub) {
		t.Error("'help' subcommand must be exempt so gosymdb help works without a database")
	}
}

func TestIsDBExemptCmdReadCommandIsNotExempt(t *testing.T) {
	root := &cobra.Command{Use: "gosymdb"}
	for _, name := range []string{"find", "callers", "callees", "def", "health", "dead", "blast-radius", "packages", "implementors"} {
		sub := &cobra.Command{Use: name}
		root.AddCommand(sub)
		if isDBExemptCmd(sub) {
			t.Errorf("read command %q must NOT be exempt from no-DB check", name)
		}
	}
}

// — writeJSONError ——————————————————————————————————————————————————————————

func captureWriteJSONError(t *testing.T, root *cobra.Command, err error) map[string]any {
	t.Helper()
	// Replace osExit with a panic so tests can recover the exit call.
	origExit := osExit
	osExit = func(code int) { panic(code) }
	t.Cleanup(func() { osExit = origExit })

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	func() {
		defer func() { recover() }()
		writeJSONError(root, err)
	}()

	w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	buf.ReadFrom(r)
	r.Close()

	var m map[string]any
	json.Unmarshal(buf.Bytes(), &m)
	return m
}

func TestWriteJSONErrorUnknownFlag(t *testing.T) {
	root := &cobra.Command{Use: "gosymdb"}
	m := captureWriteJSONError(t, root, errors.New("unknown flag: --frobnitz"))
	if m["error_code"] != "unknown_command_or_flag" {
		t.Errorf("expected error_code=unknown_command_or_flag; got %v", m)
	}
	hint, _ := m["hint"].(string)
	if !strings.Contains(hint, "hallucinating") {
		t.Errorf("expected 'hallucinating' in hint; got %q", hint)
	}
}

func TestWriteJSONErrorUnknownSubcommand(t *testing.T) {
	root := &cobra.Command{Use: "gosymdb"}
	root.AddCommand(&cobra.Command{Use: "find"})
	root.AddCommand(&cobra.Command{Use: "callers"})
	m := captureWriteJSONError(t, root, errors.New(`unknown command "frobnitz" for "gosymdb"`))
	if m["error_code"] != "unknown_command_or_flag" {
		t.Errorf("expected error_code=unknown_command_or_flag; got %v", m)
	}
	subs, _ := m["valid_subcommands"].([]any)
	if len(subs) == 0 {
		t.Errorf("expected valid_subcommands to be populated; got %v", m)
	}
}

func TestWriteJSONErrorNoDB(t *testing.T) {
	root := &cobra.Command{Use: "gosymdb"}
	m := captureWriteJSONError(t, root, errors.New("no database found (searched from /tmp to filesystem root); use --db to specify one"))
	if m["error_code"] != "no_database" {
		t.Errorf("expected error_code=no_database; got %v", m)
	}
	if m["recovery"] == nil {
		t.Errorf("expected recovery hint in JSON error; got %v", m)
	}
}

func TestWriteJSONErrorMissingFlag(t *testing.T) {
	root := &cobra.Command{Use: "gosymdb"}
	m := captureWriteJSONError(t, root, errors.New("--symbol is required"))
	if m["error_code"] != "missing_required_flag" {
		t.Errorf("expected error_code=missing_required_flag; got %v", m)
	}
	if m["flag"] != "--symbol" {
		t.Errorf("expected flag=--symbol; got %v", m)
	}
}

func TestWriteJSONErrorGenericIncludesMessage(t *testing.T) {
	root := &cobra.Command{Use: "gosymdb"}
	m := captureWriteJSONError(t, root, errors.New("something went wrong"))
	if m["error"] != "something went wrong" {
		t.Errorf("expected error field; got %v", m)
	}
}
