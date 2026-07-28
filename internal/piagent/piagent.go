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
	"strconv"
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
		// Provider/Model identify the model that produced an assistant message.
		// Together they key the context-window lookup used for the percentage.
		Provider string `json:"provider"`
		Model    string `json:"model"`
		// Usage carries per-message token accounting. totalTokens reflects the
		// full context size after the message, so the latest non-zero value is
		// the current context consumption.
		Usage *struct {
			TotalTokens int `json:"totalTokens"`
		} `json:"usage"`
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

	// Token accounting for the heartbeat. tokens is the latest reported context
	// size; provider/model identify the active model so window can resolve the
	// context-window size for the percentage.
	tokens   int
	provider string
	model    string
	// window resolves the context-window size (in tokens) for a provider/model,
	// returning 0 when unknown. It is injected so the parser stays pure and
	// testable; production wires it to the pi model catalog.
	window func(provider, model string) int
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

	// Fold in token usage from any assistant message that reports it. The first
	// message_start carries an all-zero usage, so only non-zero totals update
	// the running consumption.
	if e.Message != nil && e.Message.Role == "assistant" && e.Message.Usage != nil && e.Message.Usage.TotalTokens > 0 {
		p.tokens = e.Message.Usage.TotalTokens
		if e.Message.Provider != "" {
			p.provider = e.Message.Provider
		}
		if e.Message.Model != "" {
			p.model = e.Message.Model
		}
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

// render returns the entire progress transcript up to now, ending with the
// current token consumption when known.
func (p *progress) render() string {
	lines := make([]string, 0, len(p.steps)+2)
	lines = append(lines, p.steps...)
	if p.writing != "" {
		lines = append(lines, "writing: "+p.writing)
	}
	if s := p.contextLine(); s != "" {
		lines = append(lines, s)
	}
	return strings.Join(lines, "\n")
}

// contextLine formats the current token consumption for the heartbeat: the
// absolute token count and, when the context window is known, the percentage
// of that window in use. Returns "" before any usage has been reported.
func (p *progress) contextLine() string {
	if p.tokens <= 0 {
		return ""
	}
	var w int
	if p.window != nil {
		w = p.window(p.provider, p.model)
	}
	if w > 0 {
		pct := float64(p.tokens) / float64(w) * 100
		return fmt.Sprintf("context: %s tokens (%.1f%% of %s)", groupThousands(p.tokens), pct, groupThousands(w))
	}
	return fmt.Sprintf("context: %s tokens", groupThousands(p.tokens))
}

// groupThousands formats n with comma thousands separators (e.g. 18477 ->
// "18,477") for readable heartbeat output.
func groupThousands(n int) string {
	s := strconv.Itoa(n)
	neg := ""
	if n < 0 {
		neg, s = "-", s[1:]
	}
	var b strings.Builder
	for i, c := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(c)
	}
	return neg + b.String()
}

// Agent adapts Run to an interface (e.g. codereview.Agent) so it can be
// injected into workflows that need a coding agent.
type Agent struct{}

// Run runs the Pi agent for prompt in workDir. See the package-level Run.
func (Agent) Run(ctx context.Context, prompt, workDir string) (string, error) {
	return Run(ctx, prompt, workDir)
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

	// Feed the prompt via stdin rather than as a positional argument. Pi's `-p`
	// is a boolean flag and the prompt is a positional message, so a prompt that
	// begins with "-" (e.g. a bullet list) is otherwise parsed as an unknown
	// option and Pi exits with an error. Piped stdin is read as the initial
	// message before argument parsing matters, so any prompt text is safe.
	//
	// Always send the original prompt, even on a retry. If the earlier attempt
	// got far enough to record it, Pi resumes with full context and continues;
	// if it died before the prompt reached the session (or before the session
	// existed at all), the retry still has the task to work from. A bare
	// "Continue" would break that second case.
	cmd := exec.CommandContext(ctx, "pi", "-p", "--mode", "json", "--session-id", sessionID)
	cmd.Dir = workDir
	cmd.Stdin = strings.NewReader(prompt)

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

	// Resolve context-window sizes from the pi model catalog for the token
	// percentage. Warm the cache up front so the first usage event renders a
	// percentage without blocking the stream loop on the catalog subprocess.
	go warmContextWindows()

	prog := progress{window: contextWindowFor}
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
