package cmd

import (
	"fmt"
	"strings"
)

// qaOrchestratorProtocol is the built-in QA protocol for the orchestrator agent.
const qaOrchestratorProtocol = `You are a QA automation agent running in an isolated container.
Execute the test plan below and report results.

## Verification Toolkit
- Run commands and check output/exit codes
- Read files to verify content
- Use project-specific tools (see extra instructions below)
- For h2 testing: h2 list, h2 peek, h2 send, session logs
- Save evidence to ~/results/evidence/ (screenshots, logs, diffs)

## Timeout Rules
- If an operation does not complete within 60 seconds, mark FAIL and move on
- Use polling loops with max iterations (e.g., check every 5s, max 12 times)
- If cleanup fails, note it in the report and continue

## How to Test
1. Read the test plan
2. For each test case:
   a. Set up prerequisites
   b. Execute test steps
   c. Verify expected outcomes
   d. Record PASS/FAIL/SKIP with details and timing
   e. Clean up for next test
3. Write results to ~/results/report.md and ~/results/metadata.json

## Result Format

### ~/results/report.md
Write a human-readable report with:
- Summary (total pass/fail/skip)
- Per-test-case details with timing
- Any evidence references

### ~/results/metadata.json
Write machine-readable JSON:
{
  "plan": "<plan-name>",
  "started_at": "<ISO8601>",
  "finished_at": "<ISO8601>",
  "duration_seconds": <int>,
  "total": <int>,
  "pass": <int>,
  "fail": <int>,
  "skip": <int>,
  "exit_reason": "completed"
}

## Cost Guidance
- Use cheaper models (sonnet/haiku) for sub-agents when possible
- Only use opus for complex reasoning tasks`

// GenerateOrchestratorInstructions returns the plain-text instructions string
// for the QA orchestrator (protocol + extra instructions + test plan).
// This is used with claude --system-prompt.
func GenerateOrchestratorInstructions(extraInstructions, testPlanContent string) string {
	var b strings.Builder

	b.WriteString(qaOrchestratorProtocol)

	if extraInstructions != "" {
		b.WriteString("\n\n## Project-Specific Instructions\n\n")
		b.WriteString(strings.TrimSpace(extraInstructions))
	}

	b.WriteString("\n\n## Test Plan\n\n")
	b.WriteString(strings.TrimSpace(testPlanContent))
	b.WriteString("\n")

	return b.String()
}

// GenerateOrchestratorRole generates the QA orchestrator role YAML content.
// Used when writing a role file (e.g., for --no-docker with h2 run --role).
func GenerateOrchestratorRole(model, extraInstructions, testPlanContent, planName string) string {
	instructions := GenerateOrchestratorInstructions(extraInstructions, testPlanContent)

	// Build YAML. We use fmt.Sprintf with a block scalar (|) for the instructions
	// to preserve newlines and avoid YAML escaping issues.
	yaml := fmt.Sprintf(`name: qa-orchestrator
model: %s
permission_mode: bypassPermissions
instructions: |
%s`, model, indentBlock(instructions, "  "))

	return yaml
}

// indentBlock indents every line of text with the given prefix.
func indentBlock(text, prefix string) string {
	lines := strings.Split(text, "\n")
	var result strings.Builder
	for i, line := range lines {
		if i > 0 {
			result.WriteString("\n")
		}
		if line == "" {
			result.WriteString(prefix)
		} else {
			result.WriteString(prefix)
			result.WriteString(line)
		}
	}
	return result.String()
}
