package agents

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
)

// claudeCodePermissionMode controls how much autonomy the spawned Claude
// Code CLI is given inside a cloned repo. "acceptEdits" lets it read and
// write files without a human in the loop while still gating riskier tools
// (e.g. arbitrary Bash). Override with CLAUDE_CODE_PERMISSION_MODE if a repo
// needs to run its own tooling (tests, formatters, etc) as part of a fix -
// e.g. "bypassPermissions" for full autonomy in a disposable sandbox.
func claudeCodePermissionMode() string {
	if mode := os.Getenv("CLAUDE_CODE_PERMISSION_MODE"); mode != "" {
		return mode
	}
	return "acceptEdits"
}

// RunClaudeCode invokes the Claude Code CLI, non-interactively, against a
// local repository checkout, letting it read and edit files in an attempt
// to resolve prompt. It returns Claude's final text response.
func RunClaudeCode(ctx context.Context, repoPath, prompt string) (string, error) {
	cmd := exec.CommandContext(ctx, "claude",
		"-p", prompt,
		"--permission-mode", claudeCodePermissionMode(),
	)
	cmd.Dir = repoPath
	cmd.Env = os.Environ()

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("claude code: %w: %s", err, stderr.String())
	}
	return stdout.String(), nil
}
