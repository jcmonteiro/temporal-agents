// Package hubclient provides the HTTP adapter used by CLI commands that read the
// Agent Hub API.
package hubclient

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
)

const (
	collectionLimit  = 200
	maxResponseBytes = 2 << 20
)

// WorkItem is the common part of an Agent Hub overview resource that the CLI
// needs. Kind contains the run type for runs, so existing CLI labels such as
// "review" and "develop" remain useful.
type WorkItem struct {
	ID     string
	Kind   string
	Label  string
	Status string
}

// Client reads the versioned Agent Hub API.
type Client struct {
	baseURL    *url.URL
	authToken  string
	httpClient *http.Client
}

// New validates the API endpoint and builds a client. apiURL must point at the
// versioned API root, for example http://127.0.0.1:8973/api/v1.
func New(apiURL, authToken string, httpClient *http.Client) (*Client, error) {
	endpoint, err := url.Parse(strings.TrimSpace(apiURL))
	if err != nil {
		return nil, fmt.Errorf("the Agent Hub API endpoint must be an absolute http or https URL: %w", err)
	}
	if (endpoint.Scheme != "http" && endpoint.Scheme != "https") || endpoint.Host == "" {
		return nil, errors.New("the Agent Hub API endpoint must be an absolute http or https URL")
	}
	if endpoint.User != nil || endpoint.RawQuery != "" || endpoint.Fragment != "" {
		return nil, errors.New("the Agent Hub API endpoint must not contain credentials, a query, or a fragment")
	}
	if endpoint.Scheme == "http" && !isLoopbackHost(endpoint.Hostname()) {
		return nil, errors.New("a non-loopback Agent Hub API endpoint must use HTTPS")
	}
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	endpoint.Path = strings.TrimRight(endpoint.Path, "/")
	return &Client{
		baseURL:    endpoint,
		authToken:  strings.TrimSpace(authToken),
		httpClient: httpClient,
	}, nil
}

func isLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// Overview reads fleets, standalone run chains, and schedules in the order used
// by the CLI. The API is an overview, so each collection uses its published
// maximum page size.
func (c *Client) Overview(ctx context.Context) ([]WorkItem, error) {
	fleets, err := c.collection(ctx, "fleets")
	if err != nil {
		return nil, err
	}
	runs, err := c.collection(ctx, "runs")
	if err != nil {
		return nil, err
	}
	schedules, err := c.collection(ctx, "schedules")
	if err != nil {
		return nil, err
	}

	items := make([]WorkItem, 0, len(fleets)+len(runs)+len(schedules))
	for _, item := range fleets {
		items = append(items, WorkItem{ID: item.ID, Kind: item.Kind, Label: item.Label, Status: item.Status})
	}
	for _, item := range runs {
		kind := item.Type
		if kind == "" {
			kind = item.Kind
		}
		items = append(items, WorkItem{ID: item.ID, Kind: kind, Label: item.Label, Status: item.Status})
	}
	for _, item := range schedules {
		items = append(items, WorkItem{ID: item.ID, Kind: item.Kind, Label: item.Label, Status: item.Status})
	}
	return items, nil
}

type resource struct {
	ID     string `json:"id"`
	Kind   string `json:"kind"`
	Type   string `json:"type"`
	Label  string `json:"label"`
	Status string `json:"status"`
}

type collection struct {
	Items []resource `json:"items"`
}

type problem struct {
	Title  string `json:"title"`
	Detail string `json:"detail"`
}

func (c *Client) collection(ctx context.Context, name string) ([]resource, error) {
	endpoint := *c.baseURL
	endpoint.Path += "/" + name
	query := endpoint.Query()
	query.Set("limit", fmt.Sprint(collectionLimit))
	endpoint.RawQuery = query.Encode()

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("build Agent Hub %s request: %w", name, err)
	}
	request.Header.Set("Accept", "application/json")
	if c.authToken != "" {
		request.Header.Set("Authorization", "Bearer "+c.authToken)
	}

	response, err := c.httpClient.Do(request)
	if err != nil {
		return nil, fmt.Errorf("read Agent Hub %s: %w", name, err)
	}
	defer response.Body.Close()

	body, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read Agent Hub %s response: %w", name, err)
	}
	if len(body) > maxResponseBytes {
		return nil, fmt.Errorf("Agent Hub %s response exceeds %d bytes", name, maxResponseBytes)
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return nil, apiError(name, response.StatusCode, body)
	}

	var document collection
	if err := json.Unmarshal(body, &document); err != nil {
		return nil, fmt.Errorf("decode Agent Hub %s response: %w", name, err)
	}
	if document.Items == nil {
		document.Items = []resource{}
	}
	return document.Items, nil
}

func apiError(resourceName string, status int, body []byte) error {
	var document problem
	if err := json.Unmarshal(body, &document); err == nil {
		message := strings.TrimSpace(document.Detail)
		if message == "" {
			message = strings.TrimSpace(document.Title)
		}
		if message != "" {
			return fmt.Errorf("Agent Hub %s returned HTTP %d: %s", resourceName, status, message)
		}
	}
	return fmt.Errorf("Agent Hub %s returned HTTP %d", resourceName, status)
}
