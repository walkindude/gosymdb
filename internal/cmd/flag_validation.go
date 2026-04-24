package cmd

import (
	"fmt"
	"strings"
)

var (
	findKinds = []string{"func", "method", "type", "interface", "var", "const"}
	deadKinds = []string{"func", "method"}
	refKinds  = []string{"type_assert", "type_switch", "composite_lit", "conversion", "field_access", "embed"}
)

type invalidFlagValueError struct {
	Flag  string
	Value string
	Valid []string
}

func (e *invalidFlagValueError) Error() string {
	return fmt.Sprintf("invalid value for %s: %q (valid: %s)", e.Flag, e.Value, strings.Join(e.Valid, ", "))
}

func validateEnumFlag(flag, value string, valid []string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	for _, candidate := range valid {
		if value == candidate {
			return nil
		}
	}
	return &invalidFlagValueError{Flag: flag, Value: value, Valid: valid}
}

func stripTypeInstantiationArgs(name string) string {
	var b strings.Builder
	depth := 0
	for _, r := range name {
		switch r {
		case '[':
			depth++
		case ']':
			if depth > 0 {
				depth--
			}
		default:
			if depth == 0 {
				b.WriteRune(r)
			}
		}
	}
	return b.String()
}
