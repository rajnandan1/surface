package api

import "fmt"

type RunRequest struct {
	Target string            `json:"target"`
	Suites map[string]string `json:"suites"`
}

type RunResult struct {
	ID     string         `json:"id"`
	State  string         `json:"state"`
	Reason string         `json:"reason,omitempty"`
	Caller Caller         `json:"caller"`
	Target string         `json:"target"`
	Suites []*SuiteResult `json:"suites"`
}

type Caller struct {
	Repo        string `json:"repo"`
	Ref         string `json:"ref"`
	SHA         string `json:"sha"`
	RunID       string `json:"run_id"`
	WorkflowRef string `json:"workflow_ref"`
}

type SuiteResult struct {
	Path    string        `json:"path"`
	State   string        `json:"state"`
	Failure string        `json:"failure,omitempty"`
	Tests   []*TestResult `json:"tests,omitempty"`
}

type TestResult struct {
	Name    string         `json:"name"`
	State   string         `json:"state"`
	Failure string         `json:"failure,omitempty"`
	Cleanup *CleanupResult `json:"cleanup,omitempty"`
}

type CleanupResult struct {
	State   string `json:"state"`
	Failure string `json:"failure,omitempty"`
}

func IsFinal(state string) bool {
	switch state {
	case "passed", "failed", "error", "cancelled":
		return true
	}
	return false
}

type Counts struct{ Passed, Failed, Skipped, CleanupFailed int }

func CountTests(suites ...*SuiteResult) Counts {
	var c Counts
	for _, suite := range suites {
		for _, t := range suite.Tests {
			switch t.State {
			case "passed":
				c.Passed++
			case "failed":
				c.Failed++
			case "skipped":
				c.Skipped++
			}
			if t.Cleanup != nil && t.Cleanup.State == "failed" {
				c.CleanupFailed++
			}
		}
	}
	return c
}

func (c Counts) String() string {
	text := fmt.Sprintf("%d passed", c.Passed)
	if c.Failed > 0 {
		text += fmt.Sprintf(", %d failed", c.Failed)
	}
	if c.Skipped > 0 {
		text += fmt.Sprintf(", %d skipped", c.Skipped)
	}
	if c.CleanupFailed > 0 {
		text += fmt.Sprintf(", %d cleanup failed", c.CleanupFailed)
	}
	return text
}
