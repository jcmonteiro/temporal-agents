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
	maxOverviewPages = 1000
	maxOverviewItems = maxOverviewPages * collectionLimit
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

// Overview reads every page of the additive active-work resource.
func (c *Client) Overview(ctx context.Context) ([]workoverview.Item, error) {
	resources, err := c.collection(ctx)
	if err != nil {
		return nil, err
	}
	items := make([]workoverview.Item, 0, len(resources))
	for _, item := range resources {
		items = append(items, workoverview.Item{
			ID: item.ID, Kind: workoverview.Kind(item.Type),
			Status: workoverview.Status(item.Status), Running: *item.Running,
		})
	}
	return items, nil
}

type resource struct {
	ID      string `json:"id"`
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

func (c *Client) collection(ctx context.Context) ([]resource, error) {
	const name = "active-work"
	endpoint := *c.baseURL
	endpoint.Path += "/" + name
	query := endpoint.Query()
	query.Set("limit", fmt.Sprint(collectionLimit))
	endpoint.RawQuery = query.Encode()

	var items []resource
	seenURLs := map[string]bool{}
	for range maxOverviewPages {
		if seenURLs[endpoint.String()] {
			return nil, fmt.Errorf("Agent Hub %s pagination contains a cycle", name)
		}
		seenURLs[endpoint.String()] = true
		document, err := c.readCollectionPage(ctx, name, &endpoint)
		if err != nil {
			return nil, err
		}
		if len(items)+len(document.Items) > maxOverviewItems {
			return nil, fmt.Errorf("Agent Hub %s response exceeds the item limit", name)
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
	return nil, fmt.Errorf("Agent Hub %s pagination exceeds the page limit", name)
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
		if item.Running == nil {
			return fmt.Errorf("Agent Hub %s item %d is incomplete", name, i)
		}
		overviewItem := workoverview.Item{
			ID: item.ID, Kind: workoverview.Kind(item.Type),
			Status: workoverview.Status(item.Status), Running: *item.Running,
		}
		if err := workoverview.ValidateItem(overviewItem); err != nil {
			return fmt.Errorf("Agent Hub %s item %d is invalid: %w", name, i, err)
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
	if query.Get("limit") != fmt.Sprint(collectionLimit) || strings.TrimSpace(query.Get("cursor")) == "" {
		return nil, fmt.Errorf("Agent Hub %s response has an invalid next link", name)
	}
	return next, nil
}

func apiError(resourceName string, status int, _ []byte) error {
	// Problem text is controlled by the remote endpoint and can contain terminal
	// control sequences. Keep CLI-facing errors local and fixed.
	return fmt.Errorf("Agent Hub %s returned HTTP %d", resourceName, status)
}
