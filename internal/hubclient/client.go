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

	"temporal-agents/internal/workoverview"
)

const (
	collectionLimit  = 200
	maxResponseBytes = 2 << 20
)

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
	// Redirects are not part of the Agent Hub contract. Refusing them also makes it
	// impossible to forward the bearer token to plaintext HTTP or another host.
	safeClient := *httpClient
	safeClient.CheckRedirect = func(_ *http.Request, _ []*http.Request) error {
		return http.ErrUseLastResponse
	}
	endpoint.Path = strings.TrimRight(endpoint.Path, "/")
	return &Client{
		baseURL:    endpoint,
		authToken:  strings.TrimSpace(authToken),
		httpClient: &safeClient,
	}, nil
}

func isLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// Overview reads every page of active fleets, active standalone run chains, and
// schedules in the order used by the CLI.
func (c *Client) Overview(ctx context.Context) ([]workoverview.Item, error) {
	fleets, err := c.collection(ctx, "fleets", true)
	if err != nil {
		return nil, err
	}
	runs, err := c.collection(ctx, "runs", true)
	if err != nil {
		return nil, err
	}
	schedules, err := c.collection(ctx, "schedules", false)
	if err != nil {
		return nil, err
	}

	items := make([]workoverview.Item, 0, len(fleets)+len(runs)+len(schedules))
	for _, item := range fleets {
		items = append(items, workoverview.Item{
			ID: item.ID, Kind: workoverview.KindFleet,
			Status: workoverview.Status(item.Status), Running: *item.Running,
		})
	}
	for _, item := range runs {
		items = append(items, workoverview.Item{
			ID: item.ID, Kind: workoverview.Kind(item.Type),
			Status: workoverview.Status(item.Status), Running: *item.Running,
		})
	}
	for _, item := range schedules {
		items = append(items, workoverview.Item{
			ID: item.ID, Kind: workoverview.KindSchedule,
			Status: workoverview.Status(item.Status),
		})
	}
	return items, nil
}

type resource struct {
	ID      string `json:"id"`
	Kind    string `json:"kind"`
	Type    string `json:"type"`
	Status  string `json:"status"`
	Running *bool  `json:"running"`
}

type collection struct {
	Items []resource     `json:"items"`
	Count int            `json:"count"`
	Limit int            `json:"limit"`
	Next  nullableString `json:"next"`
}

type nullableString struct {
	Set   bool
	Value *string
}

func (value *nullableString) UnmarshalJSON(data []byte) error {
	value.Set = true
	if string(data) == "null" {
		value.Value = nil
		return nil
	}
	var decoded string
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	value.Value = &decoded
	return nil
}

type problem struct {
	Title  string `json:"title"`
	Detail string `json:"detail"`
}

func (c *Client) collection(ctx context.Context, name string, active bool) ([]resource, error) {
	endpoint := *c.baseURL
	endpoint.Path += "/" + name
	query := endpoint.Query()
	query.Set("limit", fmt.Sprint(collectionLimit))
	if active {
		query.Set("active", "true")
	}
	endpoint.RawQuery = query.Encode()

	var items []resource
	seenURLs := map[string]bool{}
	for {
		if seenURLs[endpoint.String()] {
			return nil, fmt.Errorf("Agent Hub %s pagination contains a cycle", name)
		}
		seenURLs[endpoint.String()] = true
		document, err := c.readCollectionPage(ctx, name, &endpoint)
		if err != nil {
			return nil, err
		}
		items = append(items, document.Items...)
		if document.Next.Value == nil {
			return items, nil
		}
		next, err := c.nextPageURL(name, &endpoint, *document.Next.Value)
		if err != nil {
			return nil, err
		}
		endpoint = *next
	}
}

func (c *Client) readCollectionPage(ctx context.Context, name string, endpoint *url.URL) (collection, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return collection{}, fmt.Errorf("build Agent Hub %s request: %w", name, err)
	}
	request.Header.Set("Accept", "application/json")
	if c.authToken != "" {
		request.Header.Set("Authorization", "Bearer "+c.authToken)
	}

	response, err := c.httpClient.Do(request)
	if err != nil {
		return collection{}, fmt.Errorf("read Agent Hub %s: %w", name, err)
	}
	defer response.Body.Close()

	body, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if err != nil {
		return collection{}, fmt.Errorf("read Agent Hub %s response: %w", name, err)
	}
	if len(body) > maxResponseBytes {
		return collection{}, fmt.Errorf("Agent Hub %s response exceeds %d bytes", name, maxResponseBytes)
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return collection{}, apiError(name, response.StatusCode, body)
	}

	var document collection
	if err := json.Unmarshal(body, &document); err != nil {
		return collection{}, fmt.Errorf("decode Agent Hub %s response: %w", name, err)
	}
	if err := validateCollection(name, document); err != nil {
		return collection{}, err
	}
	return document, nil
}

func validateCollection(name string, document collection) error {
	if document.Items == nil {
		return fmt.Errorf("Agent Hub %s response is missing items", name)
	}
	if document.Count != len(document.Items) {
		return fmt.Errorf("Agent Hub %s response count does not match items", name)
	}
	if document.Limit < 1 || document.Limit > collectionLimit || len(document.Items) > document.Limit {
		return fmt.Errorf("Agent Hub %s response has an invalid limit", name)
	}
	if !document.Next.Set || document.Next.Value != nil && strings.TrimSpace(*document.Next.Value) == "" {
		return fmt.Errorf("Agent Hub %s response has an invalid next link", name)
	}
	for i, item := range document.Items {
		if item.ID == "" || item.Kind == "" || item.Status == "" {
			return fmt.Errorf("Agent Hub %s item %d is incomplete", name, i)
		}
		if !workoverview.ValidStatus(workoverview.Status(item.Status)) {
			return fmt.Errorf("Agent Hub %s item %d has unknown status %q", name, i, item.Status)
		}
		switch name {
		case "fleets":
			if item.Kind != "fleet" || item.Running == nil || !*item.Running {
				return fmt.Errorf("Agent Hub %s item %d violates the fleet contract", name, i)
			}
		case "runs":
			kind := workoverview.Kind(item.Type)
			if item.Kind != "run" || item.Running == nil || !*item.Running || kind == workoverview.KindFleet ||
				kind == workoverview.KindSchedule || !workoverview.ValidKind(kind) {
				return fmt.Errorf("Agent Hub %s item %d violates the run contract", name, i)
			}
		case "schedules":
			if item.Kind != "schedule" {
				return fmt.Errorf("Agent Hub %s item %d violates the schedule contract", name, i)
			}
		default:
			return fmt.Errorf("unknown Agent Hub collection %q", name)
		}
	}
	return nil
}

func (c *Client) nextPageURL(name string, current *url.URL, reference string) (*url.URL, error) {
	next, err := url.Parse(strings.TrimSpace(reference))
	if err != nil {
		return nil, fmt.Errorf("Agent Hub %s response has an invalid next link: %w", name, err)
	}
	next = current.ResolveReference(next)
	if next.Scheme != c.baseURL.Scheme || !strings.EqualFold(next.Host, c.baseURL.Host) ||
		next.User != nil || next.Fragment != "" || next.Path != c.baseURL.Path+"/"+name {
		return nil, fmt.Errorf("Agent Hub %s response has an unsafe next link", name)
	}
	query := next.Query()
	if query.Get("limit") != fmt.Sprint(collectionLimit) || strings.TrimSpace(query.Get("cursor")) == "" ||
		(name == "fleets" || name == "runs") && query.Get("active") != "true" ||
		name == "schedules" && query.Get("active") != "" {
		return nil, fmt.Errorf("Agent Hub %s response has an invalid next link", name)
	}
	return next, nil
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
