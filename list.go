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
	"temporal-agents/internal/workoverview"
)

const (
	defaultAgentHubAPIURL = "http://127.0.0.1:3000/api/v1"
	agentHubAPIURLEnv     = "AGENT_HUB_API_URL"
	cliHTTPTimeout        = 35 * time.Second
	overviewTimeout       = 35 * time.Second
)

// listCmd reads the Agent Hub overview and prints the active top-level work.
func listCmd(ctx context.Context, out io.Writer, reader workoverview.Reader) error {
	items, err := reader.Overview(ctx)
	if err != nil {
		return fmt.Errorf("could not list work through Agent Hub: %w", err)
	}
	for index, item := range items {
		if err := workoverview.ValidateItem(item); err != nil {
			return fmt.Errorf("could not list work through Agent Hub: item %d: %w", index, err)
		}
	}
	if _, err := fmt.Fprint(out, formatActiveWork(items)); err != nil {
		return fmt.Errorf("write active work: %w", err)
	}
	return nil
}

// listRunning is the command composition root. The CLI uses the same bearer token as
// remote Agent Hub consumers and the safe loopback endpoint by default; on this
// machine the token is the one `serve` minted, so reading the hub stays a single
// command with no credential to copy (see apitoken.go).
func listRunning() {
	apiURL := strings.TrimSpace(os.Getenv(agentHubAPIURLEnv))
	if apiURL == "" {
		apiURL = defaultAgentHubAPIURL
	}
	reader, err := hubclient.New(apiURL, apiToken(os.Getenv(agentHubAuthTokenEnv)), &http.Client{Timeout: cliHTTPTimeout})
	if err != nil {
		fatalf("%v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), overviewTimeout)
	defer cancel()
	if err := listCmd(ctx, os.Stdout, reader); err != nil {
		fatalf("%v", err)
	}
}

// formatActiveWork preserves list's active-work meaning over the Agent Hub model:
// unsettled executions are active, while every recurring schedule is active
// configuration and remains visible regardless of its latest outcome.
func formatActiveWork(items []workoverview.Item) string {
	active := make([]workoverview.Item, 0, len(items))
	for _, item := range items {
		if item.Kind == workoverview.KindSchedule || item.Running {
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
