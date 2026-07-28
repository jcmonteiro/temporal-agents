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

// piArgs builds the pi CLI arguments for a run. Kept separate from Run so the
// argument wiring is unit-testable without spawning pi.
func piArgs(sessionID string) []string {
	return []string{"-p", "--mode", "json", "--session-id", sessionID}
}

// maxResumes bounds how many times Run resumes the session after a threshold
// auto-compaction, so a task that keeps producing over-threshold context cannot
// loop forever burning tokens. The cap is generous: real work finishes well
// within it, while a pathological loop is stopped.
const maxResumes = 50

// continueMessage is fed to the resumed session after a threshold compaction.
// Phrased as an instruction to continue the in-flight task rather than start
// something new.
const continueMessage = "Your context was automatically compacted to free space. Continue the task from where you left off; do not restart or wait for further input."

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
	// Compaction fields, present on compaction_end. reason is "threshold",
	// "overflow", or "manual". A successful, non-retrying threshold compaction
	// stops the run in -p mode, which Run detects to resume the session.
	Reason       string `json:"reason"`
	WillRetry    bool   `json:"willRetry"`
	Aborted      bool   `json:"aborted"`
	ErrorMessage string `json:"errorMessage"`
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

	// thresholdCompacted records that the run *trailed off* with a successful,
	// non-retrying threshold auto-compaction: the compaction was the last
	// meaningful activity, which in -p mode ends the run without finishing the
	// task, so Run resumes the session when it is set. Pi can, however, continue
	// after a compaction when an extension queues a message; any subsequent
	// agent/message/tool activity therefore clears the flag so a compaction that
	// was followed by more work is not mistaken for a trailing one.
	thresholdCompacted bool
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

	// Any turn/message/tool activity means the run did not trail off with the
	// compaction: Pi continued (e.g. an extension queued a message), so a
	// previously recorded threshold compaction is no longer the terminal event.
	switch e.Type {
	case "turn_start", "message_start", "message_update", "message_end",
		"tool_execution_start", "tool_execution_end":
		p.thresholdCompacted = false
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
	case "compaction_start":
		p.add("compacting context…")
	case "compaction_end":
		switch {
		case e.Aborted:
			p.add("compaction aborted")
		case e.ErrorMessage != "":
			p.add("compaction failed")
		default:
			p.add("compaction complete")
			if e.Reason == "threshold" && !e.WillRetry {
				p.thresholdCompacted = true
			}
		}
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

	// Resolve context-window sizes from the pi model catalog for the token
	// percentage. Warm the cache up front so the first usage event renders a
	// percentage without blocking the stream loop on the catalog subprocess.
	go warmContextWindows()

	args := piArgs(sessionID)
	return runLoop(ctx, runOnce, args, workDir, prompt)
}

// runOnceFunc runs a single pi invocation. It is the seam runLoop is written
// against so the resume loop can be tested without spawning pi.
type runOnceFunc func(ctx context.Context, args []string, workDir, input string) (result string, thresholdCompacted bool, err error)

// runLoop drives the resume loop: it invokes run with the original prompt, then
// repeatedly resumes the same session with continueMessage as long as a run
// ends with a trailing threshold auto-compaction, returning the last non-empty
// final message. Bounded by maxResumes so a task that keeps producing
// over-threshold context cannot loop forever.
func runLoop(ctx context.Context, run runOnceFunc, args []string, workDir, prompt string) (string, error) {
	// First invocation sends the original prompt; see runOnce for why via stdin.
	//
	// Always send the original prompt on the first invocation, even on an
	// activity retry. If the earlier attempt got far enough to record it, Pi
	// resumes with full context and continues; if it died before the prompt
	// reached the session (or before the session existed at all), the retry
	// still has the task to work from. A bare "Continue" would break that case.
	input := prompt

	var lastResult string
	for i := 0; ; i++ {
		result, thresholdCompacted, err := run(ctx, args, workDir, input)
		if err != nil {
			return "", err
		}
		if result != "" {
			lastResult = result
		}

		// A threshold auto-compaction stops Pi in -p mode without finishing the
		// task (unlike an overflow compaction, which auto-retries inside Pi).
		// Resume the same session with a continue instruction so the agent keeps
		// working with the freshly compacted context. Resuming reuses the same
		// session id, which loads the compacted history; the continue message is
		// appended as the next turn. Bounded by maxResumes.
		if !thresholdCompacted {
			break
		}
		// The run ended mid-task on a threshold compaction. If the resume cap is
		// exhausted, the task is known to be unfinished: fail rather than return
		// partial output as success, so Temporal does not mark the activity
		// complete and let downstream workflows proceed with a truncated result.
		if i >= maxResumes {
			return "", fmt.Errorf("pi resume cap (%d) exhausted while the run still ended in a threshold compaction; task unfinished", maxResumes)
		}
		input = continueMessage
	}
	return lastResult, nil
}

// runOnce runs a single pi invocation with input on stdin, streaming Pi's JSON
// events as Temporal heartbeat details. It returns the final assistant message
// and whether the run ended with a successful threshold auto-compaction (which
// stops Pi in -p mode and signals Run to resume the session).
func runOnce(ctx context.Context, args []string, workDir, input string) (result string, thresholdCompacted bool, err error) {
	// Feed input via stdin rather than as a positional argument. Pi's `-p` is a
	// boolean flag and the message is positional, so an input that begins with
	// "-" (e.g. a bullet list) is otherwise parsed as an unknown option and Pi
	// exits with an error. Piped stdin is read as the initial message before
	// argument parsing matters, so any input text is safe.
	cmd := exec.CommandContext(ctx, "pi", args...)
	cmd.Dir = workDir
	cmd.Stdin = strings.NewReader(input)

	// When the activity is cancelled (heartbeat timeout or worker shutdown),
	// interrupt Pi rather than SIGKILLing it immediately, giving it a chance to
	// flush its session file cleanly before the WaitDelay forces termination.
	cmd.Cancel = func() error { return cmd.Process.Signal(syscall.SIGINT) }
	cmd.WaitDelay = 10 * time.Second

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", false, fmt.Errorf("pipe pi stdout: %w", err)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Start(); err != nil {
		return "", false, fmt.Errorf("start pi: %w", err)
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
		return "", false, fmt.Errorf("pi failed: %w\n%s", waitErr, strings.TrimSpace(stderr.String()))
	}

	result = strings.TrimSpace(finalMsg.String())
	if result == "" {
		result = strings.TrimSpace(prog.writing)
	}
	return result, prog.thresholdCompacted, nil
}

func truncate(s string, max int) string {
	s = strings.TrimSpace(s)
	if len(s) <= max {
		return s
	}
	return s[:max-1] + "…"
}
