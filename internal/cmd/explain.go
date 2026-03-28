package cmd

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

type explainPayload struct {
	Command         string         `json:"command"`
	Input           string         `json:"input,omitempty"`
	ResolvedSymbol  string         `json:"resolved_symbol,omitempty"`
	Resolution      string         `json:"resolution,omitempty"`
	Mode            string         `json:"mode,omitempty"`
	NormalizedQuery string         `json:"normalized_query,omitempty"`
	Filters         map[string]any `json:"filters,omitempty"`
	Traversal       map[string]any `json:"traversal,omitempty"`
	Notes           []string       `json:"notes,omitempty"`
}

func addExplain(payload map[string]any, explain *explainPayload) {
	if explain != nil {
		payload["explain"] = explain
	}
}

func formatExplainText(explain *explainPayload) string {
	if explain == nil {
		return ""
	}

	var b strings.Builder
	b.WriteString("explain:\n")
	b.WriteString(fmt.Sprintf("  command: %s\n", explain.Command))
	if explain.Input != "" {
		b.WriteString(fmt.Sprintf("  input: %s\n", explain.Input))
	}
	if explain.Mode != "" {
		b.WriteString(fmt.Sprintf("  mode: %s\n", explain.Mode))
	}
	if explain.ResolvedSymbol != "" {
		b.WriteString(fmt.Sprintf("  resolved_symbol: %s\n", explain.ResolvedSymbol))
	}
	if explain.NormalizedQuery != "" {
		b.WriteString(fmt.Sprintf("  normalized_query: %s\n", explain.NormalizedQuery))
	}
	if explain.Resolution != "" {
		b.WriteString(fmt.Sprintf("  resolution: %s\n", explain.Resolution))
	}
	writeExplainMap(&b, "filters", explain.Filters)
	writeExplainMap(&b, "traversal", explain.Traversal)
	if len(explain.Notes) > 0 {
		b.WriteString("  notes:\n")
		for _, note := range explain.Notes {
			b.WriteString(fmt.Sprintf("    - %s\n", note))
		}
	}
	return b.String()
}

func writeExplainMap(b *strings.Builder, label string, values map[string]any) {
	if len(values) == 0 {
		return
	}
	keys := make([]string, 0, len(values))
	for k := range values {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	b.WriteString(fmt.Sprintf("  %s:\n", label))
	for _, k := range keys {
		b.WriteString(fmt.Sprintf("    %s: %s\n", k, formatExplainValue(values[k])))
	}
}

func formatExplainValue(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case bool:
		if x {
			return "true"
		}
		return "false"
	case nil:
		return "null"
	default:
		buf, err := json.Marshal(x)
		if err != nil {
			return fmt.Sprint(x)
		}
		return string(buf)
	}
}
