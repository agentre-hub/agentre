package llmurl

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

const (
	defaultAnthropicBaseURL = "https://api.anthropic.com"
	defaultOpenAIBaseURL    = "https://api.openai.com/v1"
	anthropicVersion        = "2023-06-01"
)

// HTTPDoer is the HTTP boundary used for upstream LLM probes.
type HTTPDoer interface {
	Do(*http.Request) (*http.Response, error)
}

// Provider contains the execution-only provider fields needed for one upstream
// probe. Callers must not serialize this shape into an RPC response.
type Provider struct {
	Type    string
	BaseURL string
	APIKey  string
}

// Model is one upstream-discovered model. Name falls back to ModelID when the
// upstream does not provide a display name.
type Model struct {
	ModelID string
	Name    string
}

// Client sends the minimal provider probes used by execution hosts. It is a
// leaf package so daemons and desktop services can share it without importing
// either application's service layer.
type Client struct {
	http HTTPDoer
}

// NewClient constructs an upstream probe client. A nil doer uses the process
// default client.
func NewClient(doer HTTPDoer) *Client {
	if doer == nil {
		doer = http.DefaultClient
	}
	return &Client{http: doer}
}

// Test sends one minimal generation request for modelID.
func (c *Client) Test(ctx context.Context, provider Provider, modelID string) error {
	if strings.TrimSpace(modelID) == "" {
		return errors.New("model is required")
	}
	switch strings.TrimSpace(provider.Type) {
	case "anthropic":
		return c.testAnthropic(ctx, provider, modelID)
	case "openai-chat":
		return c.testOpenAIChat(ctx, provider, modelID)
	case "openai-response":
		return c.testOpenAIResponse(ctx, provider, modelID)
	default:
		return errors.New("unsupported provider type")
	}
}

// Discover returns the models exposed by the provider's model-list endpoint.
func (c *Client) Discover(ctx context.Context, provider Provider) ([]Model, error) {
	var endpointPath string
	switch strings.TrimSpace(provider.Type) {
	case "anthropic":
		endpointPath = "/v1/models"
	case "openai-chat", "openai-response":
		endpointPath = "/models"
	default:
		return nil, errors.New("unsupported provider type")
	}
	endpoint, err := Build(providerBaseURL(provider), endpointPath)
	if err != nil {
		return nil, errors.New("invalid upstream URL")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return nil, errors.New("create upstream request")
	}
	setAuth(req, provider)
	var response struct {
		Data []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"data"`
	}
	if err := c.doJSON(req, &response); err != nil {
		return nil, err
	}
	models := make([]Model, 0, len(response.Data))
	for _, item := range response.Data {
		if item.ID == "" {
			continue
		}
		name := item.Name
		if name == "" {
			name = item.ID
		}
		models = append(models, Model{ModelID: item.ID, Name: name})
	}
	return models, nil
}

func (c *Client) testAnthropic(ctx context.Context, provider Provider, modelID string) error {
	endpoint, err := Build(providerBaseURL(provider), "/v1/messages")
	if err != nil {
		return errors.New("invalid upstream URL")
	}
	req, err := newJSONRequest(ctx, endpoint.String(), map[string]any{
		"model": modelID, "max_tokens": 16,
		"messages": []map[string]string{{"role": "user", "content": "hi"}},
	})
	if err != nil {
		return errors.New("create upstream request")
	}
	setAuth(req, provider)
	var response struct {
		Content    []json.RawMessage `json:"content"`
		StopReason string            `json:"stop_reason"`
	}
	if err := c.doJSON(req, &response); err != nil {
		return err
	}
	if len(response.Content) == 0 && response.StopReason == "" {
		return errors.New("empty completion response")
	}
	return nil
}

func (c *Client) testOpenAIChat(ctx context.Context, provider Provider, modelID string) error {
	endpoint, err := Build(providerBaseURL(provider), "/chat/completions")
	if err != nil {
		return errors.New("invalid upstream URL")
	}
	req, err := newJSONRequest(ctx, endpoint.String(), map[string]any{
		"model":    modelID,
		"messages": []map[string]string{{"role": "user", "content": "hi"}},
	})
	if err != nil {
		return errors.New("create upstream request")
	}
	setAuth(req, provider)
	var response struct {
		Choices []json.RawMessage `json:"choices"`
	}
	if err := c.doJSON(req, &response); err != nil {
		return err
	}
	if len(response.Choices) == 0 {
		return errors.New("empty completion choices")
	}
	return nil
}

func (c *Client) testOpenAIResponse(ctx context.Context, provider Provider, modelID string) error {
	endpoint, err := Build(providerBaseURL(provider), "/responses")
	if err != nil {
		return errors.New("invalid upstream URL")
	}
	req, err := newJSONRequest(ctx, endpoint.String(), map[string]any{
		"model": modelID, "input": "hi", "max_output_tokens": 16,
	})
	if err != nil {
		return errors.New("create upstream request")
	}
	setAuth(req, provider)
	var response json.RawMessage
	return c.doJSON(req, &response)
}

func providerBaseURL(provider Provider) string {
	if strings.TrimSpace(provider.BaseURL) != "" {
		return provider.BaseURL
	}
	if provider.Type == "anthropic" {
		return defaultAnthropicBaseURL
	}
	return defaultOpenAIBaseURL
}

func setAuth(req *http.Request, provider Provider) {
	if provider.Type == "anthropic" {
		req.Header.Set("x-api-key", provider.APIKey)
		req.Header.Set("anthropic-version", anthropicVersion)
		return
	}
	if provider.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+provider.APIKey)
	}
}

func newJSONRequest(ctx context.Context, endpoint string, body any) (*http.Request, error) {
	encoded, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(encoded))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	return req, nil
}

func (c *Client) doJSON(req *http.Request, output any) error {
	response, err := c.http.Do(req)
	if err != nil {
		return errors.New("upstream request failed")
	}
	defer func() { _ = response.Body.Close() }()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		return errors.New("read upstream response")
	}
	if response.StatusCode/100 != 2 {
		return fmt.Errorf("upstream returned HTTP status %d", response.StatusCode)
	}
	if len(body) == 0 {
		return errors.New("empty upstream response")
	}
	if err := json.Unmarshal(body, output); err != nil {
		return errors.New("invalid upstream response")
	}
	return nil
}
