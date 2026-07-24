package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync/atomic"
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

// RunPiAgent runs the Pi agent for req.Prompt in req.WorkDir, streaming Pi's
// JSON events as Temporal heartbeat details (intermediary progress) and
// returning the final assistant message as the activity result.
func RunPiAgent(ctx context.Context, req PromptRequest) (string, error) {
	cmd := exec.CommandContext(ctx, "pi", "-p", req.Prompt, "--mode", "json", "--no-session")
	cmd.Dir = req.WorkDir

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
	// periodically re-sending the latest progress summary. The Go SDK throttles
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

	var currentText, finalMsg strings.Builder
	reader := bufio.NewReader(stdout)
	for {
		line, readErr := reader.ReadString('\n')
		if line = strings.TrimSpace(line); line != "" {
			if summary, final := interpret(line, &currentText); summary != "" {
				latest.Store(summary)
				activity.RecordHeartbeat(ctx, summary)
			} else if final != "" {
				finalMsg.Reset()
				finalMsg.WriteString(final)
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			break
		}
	}
	close(stop)

	waitErr := cmd.Wait()
	if waitErr != nil {
		return "", fmt.Errorf("pi failed: %w\n%s", waitErr, strings.TrimSpace(stderr.String()))
	}

	result := strings.TrimSpace(finalMsg.String())
	if result == "" {
		result = strings.TrimSpace(currentText.String())
	}
	return result, nil
}

// interpret parses one pi JSON event line, updating the in-progress assistant
// text. It returns a short progress summary (for heartbeats) and, when an
// assistant message completes, that message's final text.
func interpret(line string, currentText *strings.Builder) (summary, finalText string) {
	var e piEvent
	if err := json.Unmarshal([]byte(line), &e); err != nil {
		return truncate(line, 80), ""
	}

	switch e.Type {
	case "agent_start":
		return "Pi started", ""
	case "turn_start":
		return "thinking…", ""
	case "message_start":
		if e.Message != nil && e.Message.Role == "assistant" {
			currentText.Reset()
		}
		return "", ""
	case "message_update":
		if e.AssistantMessageEvent != nil && e.AssistantMessageEvent.Type == "text_delta" {
			currentText.WriteString(e.AssistantMessageEvent.Delta)
			return "writing: " + truncate(currentText.String(), 80), ""
		}
		return "", ""
	case "message_end":
		if e.Message != nil && e.Message.Role == "assistant" {
			var b strings.Builder
			for _, c := range e.Message.Content {
				if c.Type == "text" {
					b.WriteString(c.Text)
				}
			}
			return "", b.String()
		}
		return "", ""
	case "tool_execution_start":
		return "running tool: " + e.ToolName, ""
	case "tool_execution_end":
		return "finished tool: " + e.ToolName, ""
	case "agent_end":
		return "finalizing…", ""
	default:
		return "", ""
	}
}
