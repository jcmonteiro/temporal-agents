package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"temporal-agents/internal/hubclient"
)

const (
	defaultAgentHubAPIURL = "http://127.0.0.1:8973/api/v1"
	agentHubAPIURLEnv     = "AGENT_HUB_API_URL"
	cliHTTPTimeout        = 35 * time.Second
)

// overviewReader is the HTTP-facing port used by the list command.
type overviewReader interface {
	Overview(context.Context) ([]hubclient.WorkItem, error)
}

// listCmd reads the Agent Hub overview and prints the active top-level work.
func listCmd(ctx context.Context, out io.Writer, reader overviewReader) error {
	items, err := reader.Overview(ctx)
	if err != nil {
		return fmt.Errorf("could not list work through Agent Hub: %w", err)
	}
	_, err = fmt.Fprint(out, formatActiveWork(items))
	return err
}

// listRunning is the command composition root. The CLI uses the same bearer token
// as remote Agent Hub consumers and the safe loopback endpoint by default.
func listRunning() {
	apiURL := strings.TrimSpace(os.Getenv(agentHubAPIURLEnv))
	if apiURL == "" {
		apiURL = defaultAgentHubAPIURL
	}
	reader, err := hubclient.New(apiURL, os.Getenv(agentHubAuthTokenEnv), &http.Client{Timeout: cliHTTPTimeout})
	if err != nil {
		fatalf("%v", err)
	}
	if err := listCmd(context.Background(), os.Stdout, reader); err != nil {
		fatalf("%v", err)
	}
}

// formatActiveWork preserves list's active-work meaning over the Agent Hub model:
// only in-progress fleets and runs are active, while every recurring schedule is
// active configuration and remains visible regardless of its latest outcome.
func formatActiveWork(items []hubclient.WorkItem) string {
	active := make([]hubclient.WorkItem, 0, len(items))
	for _, item := range items {
		if item.Kind == "schedule" || item.Status == "in-progress" {
			active = append(active, item)
		}
	}
	if len(active) == 0 {
		return "Nothing running.\n"
	}

	var output strings.Builder
	tw := tabwriter.NewWriter(&output, 0, 0, 3, ' ', 0)
	fmt.Fprintln(tw, "TYPE\tID")
	fmt.Fprintln(tw, "────\t──")
	for _, item := range active {
		fmt.Fprintf(tw, "%s\t%s\n", item.Kind, item.ID)
	}
	_ = tw.Flush()
	fmt.Fprintf(&output, "\n%d active\n", len(active))
	return output.String()
}
