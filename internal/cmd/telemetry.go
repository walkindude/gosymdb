package cmd

import (
	"encoding/json"
	"os"
	"time"
)

type usageRecord struct {
	TS          int64    `json:"ts"`
	SessionID   string   `json:"session_id,omitempty"`
	SessionName string   `json:"session_name,omitempty"`
	Tool        string   `json:"tool"`
	Cmd         string   `json:"cmd,omitempty"`
	Argv        []string `json:"argv"`
	DurationMS  int64    `json:"duration_ms,omitempty"`
	ExitCode    int      `json:"exit_code,omitempty"`
	CWD         string   `json:"cwd,omitempty"`
	DB          string   `json:"db,omitempty"`
	GitBranch   string   `json:"git_branch,omitempty"`
}

// appendUsageLog writes one JSONL record to GOSYMDB_USAGE_LOG.
// It is a no-op if the env var is unset.
func appendUsageLog(argv []string, duration time.Duration, execErr error) {
	logPath := os.Getenv("GOSYMDB_USAGE_LOG")
	if logPath == "" {
		return
	}

	// Determine subcommand (first non-flag arg).
	subcmd := ""
	for _, a := range argv {
		if len(a) > 0 && a[0] != '-' {
			subcmd = a
			break
		}
	}

	// Skip self-logging to avoid double-counting wrapper records.
	if subcmd == "log-tool-use" {
		return
	}

	exitCode := 0
	if execErr != nil {
		exitCode = 1
	}

	cwd, _ := os.Getwd()
	branch, _ := gitOut("rev-parse", "--abbrev-ref", "HEAD")

	dbPath, _ := rootCmd.PersistentFlags().GetString("db")

	rec := usageRecord{
		TS:          time.Now().Unix(),
		SessionID:   os.Getenv("GOSYMDB_SESSION_ID"),
		SessionName: os.Getenv("GOSYMDB_SESSION_NAME"),
		Tool:        "gosymdb",
		Cmd:         subcmd,
		Argv:        argv,
		DurationMS:  duration.Milliseconds(),
		ExitCode:    exitCode,
		CWD:         cwd,
		DB:          dbPath,
		GitBranch:   branch,
	}

	writeUsageRecord(logPath, rec)
}

// appendWrapperLog writes one JSONL record for a wrapper invocation (grep/rg/find).
func appendWrapperLog(logPath, tool string, argv []string) {
	cwd, _ := os.Getwd()
	branch, _ := gitOut("rev-parse", "--abbrev-ref", "HEAD")

	rec := usageRecord{
		TS:          time.Now().Unix(),
		SessionID:   os.Getenv("GOSYMDB_SESSION_ID"),
		SessionName: os.Getenv("GOSYMDB_SESSION_NAME"),
		Tool:        tool,
		Argv:        argv,
		CWD:         cwd,
		GitBranch:   branch,
	}

	writeUsageRecord(logPath, rec)
}

func writeUsageRecord(logPath string, rec usageRecord) {
	f, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	defer f.Close()

	enc := json.NewEncoder(f)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(rec)
}
