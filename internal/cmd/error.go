package cmd

import (
	"encoding/json"
	"errors"
	"os"
	"sort"
	"strings"

	"github.com/spf13/cobra"
)

// writeJSONError emits a structured JSON error object to stdout and exits 1.
// Called from Execute() when --json is set and a command returns an error.
func writeJSONError(root *cobra.Command, err error) {
	payload := map[string]any{
		"error": err.Error(),
	}

	msg := err.Error()

	// Unknown subcommand or flag — hallucination hint.
	if strings.HasPrefix(msg, "unknown command") || strings.HasPrefix(msg, "unknown flag:") ||
		strings.HasPrefix(msg, "unknown shorthand flag:") {
		payload["error_code"] = "unknown_command_or_flag"
		payload["hint"] = "you may be hallucinating this subcommand or flag — it does not exist"
		if names := validSubcommandNames(root); len(names) > 0 {
			payload["valid_subcommands"] = names
		}
	}

	// No database found — provide recovery hint.
	if strings.Contains(msg, "no database found") {
		payload["error_code"] = "no_database"
		payload["recovery"] = "gosymdb index --root . --db gosymdb.sqlite"
	}

	// Missing required flag pattern (e.g. "--symbol is required").
	if strings.HasSuffix(msg, "is required") {
		payload["error_code"] = "missing_required_flag"
		if flag, ok := extractFlagName(msg); ok {
			payload["flag"] = flag
		}
	}

	var invalidValueErr *invalidFlagValueError
	if errors.As(err, &invalidValueErr) {
		payload["error_code"] = "invalid_flag_value"
		payload["flag"] = invalidValueErr.Flag
		payload["value"] = invalidValueErr.Value
		payload["valid_values"] = invalidValueErr.Valid
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(payload)
	osExit(1)
}

// osExit is a variable so tests can replace it without terminating the process.
var osExit = os.Exit

// validSubcommandNames returns the sorted list of non-hidden subcommand names.
func validSubcommandNames(root *cobra.Command) []string {
	names := make([]string, 0)
	for _, c := range root.Commands() {
		if !c.Hidden {
			names = append(names, c.Name())
		}
	}
	sort.Strings(names)
	return names
}

// extractFlagName parses messages like "--symbol is required" or "--q, --pkg, or --file is required".
func extractFlagName(msg string) (string, bool) {
	msg = strings.TrimSuffix(msg, " is required")
	msg = strings.TrimSpace(msg)
	if msg == "" {
		return "", false
	}
	return msg, true
}
