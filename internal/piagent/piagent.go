// Package piagent runs the Pi coding agent as a subprocess and streams its
// progress as Temporal activity heartbeats. It is the single place that knows
// how to talk to the `pi` CLI, so every workflow that needs an agent shares the
// same session-resume and heartbeat behavior.
package piagent

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"go.temporal.io/sdk/activity"
)

// piEvent is the subset of pi's --mode json events we care about.
type piEvent struct {
	Type     string `json:"type"`
	ToolName string `json:"toolName"`
	Message  *struct {
		Role    string `json:"role"`
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	} `json:"message"`
	AssistantMessageEvent *struct {
		Type  string `json:"type"`
		Delta string `json:"delta"`
	} `json:"assistantMessageEvent"`
}

// progress accumulates the full sequence of steps observed so far. render()
// returns everything up to now, so each heartbeat carries the complete
// progress transcript rather than just the latest step.
type progress struct {
	steps   []string // completed step descriptions, in order
	writing string   // the assistant message currently being streamed
}

func (p *progress) add(step string) { p.steps = append(p.steps, step) }

// apply folds one pi JSON event into the progress state and, when an assistant
// message completes, returns that message's final text.
func (p *progress) apply(line string) (finalText string) {
	var e piEvent
	if err := json.Unmarshal([]byte(line), &e); err != nil {
		p.add(truncate(line, 200))
		return ""
	}

	switch e.Type {
	case "agent_start":
		p.add("Pi started")
	case "turn_start":
		p.add("thinking…")
	case "message_start":
		if e.Message != nil && e.Message.Role == "assistant" {
			p.writing = ""
		}
	case "message_update":
		if e.AssistantMessageEvent != nil && e.AssistantMessageEvent.Type == "text_delta" {
			p.writing += e.AssistantMessageEvent.Delta
		}
	case "message_end":
		if e.Message != nil && e.Message.Role == "assistant" {
			var b strings.Builder
			for _, c := range e.Message.Content {
				if c.Type == "text" {
					b.WriteString(c.Text)
				}
			}
			text := b.String()
			p.writing = ""
			if strings.TrimSpace(text) != "" {
				p.add("assistant: " + text)
			}
			return text
		}
	case "tool_execution_start":
		p.add("running tool: " + e.ToolName)
	case "tool_execution_end":
		p.add("finished tool: " + e.ToolName)
	case "agent_end":
		p.add("finalizing…")
	}
	return ""
}

// render returns the entire progress transcript up to now.
func (p *progress) render() string {
	lines := make([]string, 0, len(p.steps)+1)
	lines = append(lines, p.steps...)
	if p.writing != "" {
		lines = append(lines, "writing: "+p.writing)
	}
	return strings.Join(lines, "\n")
}

// Run runs the Pi agent for prompt in workDir, streaming Pi's JSON events as
// Temporal heartbeat details. Each heartbeat carries the full progress
// transcript so far; the final assistant message is returned as the result.
//
// Run must be called from within a Temporal activity: it uses the workflow
// RunID as a stable Pi session id so that a retried activity resumes the same
// Pi session (with full context) instead of starting fresh. The RunID is
// constant across activity retries but changes on continue-as-new, so each
// chained iteration still gets its own fresh session.
func Run(ctx context.Context, prompt, workDir string) (string, error) {
	sessionID := activity.GetInfo(ctx).WorkflowExecution.RunID

	// Always pass the original prompt, even on a retry. If the earlier attempt
	// got far enough to record it, Pi resumes with full context and continues;
	// if it died before the prompt reached the session (or before the session
	// existed at all), the retry still has the task to work from. A bare
	// "Continue" would break that second case.
	cmd := exec.CommandContext(ctx, "pi", "-p", prompt, "--mode", "json", "--session-id", sessionID)
	cmd.Dir = workDir

	// When the activity is cancelled (heartbeat timeout or worker shutdown),
	// interrupt Pi rather than SIGKILLing it immediately, giving it a chance to
	// flush its session file cleanly before the WaitDelay forces termination.
	cmd.Cancel = func() error { return cmd.Process.Signal(syscall.SIGINT) }
	cmd.WaitDelay = 10 * time.Second

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", fmt.Errorf("pipe pi stdout: %w", err)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("start pi: %w", err)
	}

	// Keep the activity alive during quiet stretches (e.g. model thinking) by
	// periodically re-sending the latest full transcript. The Go SDK throttles
	// the actual network heartbeats, so this is cheap.
	var latest atomic.Value // string
	latest.Store("starting Pi…")
	stop := make(chan struct{})
	go func() {
		t := time.NewTicker(15 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-stop:
				return
			case <-t.C:
				activity.RecordHeartbeat(ctx, latest.Load().(string))
			}
		}
	}()

	var prog progress
	var finalMsg strings.Builder
	reader := bufio.NewReader(stdout)
	for {
		line, readErr := reader.ReadString('\n')
		if line = strings.TrimSpace(line); line != "" {
			if final := prog.apply(line); final != "" {
				finalMsg.Reset()
				finalMsg.WriteString(final)
			}
			transcript := prog.render()
			latest.Store(transcript)
			activity.RecordHeartbeat(ctx, transcript)
		}
		if readErr != nil {
			break
		}
	}
	close(stop)

	if waitErr := cmd.Wait(); waitErr != nil {
		return "", fmt.Errorf("pi failed: %w\n%s", waitErr, strings.TrimSpace(stderr.String()))
	}

	result := strings.TrimSpace(finalMsg.String())
	if result == "" {
		result = strings.TrimSpace(prog.writing)
	}
	return result, nil
}

func truncate(s string, max int) string {
	s = strings.TrimSpace(s)
	if len(s) <= max {
		return s
	}
	return s[:max-1] + "…"
}
