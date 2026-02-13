package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

const defaultBaseURL = "https://api.openai.com/v1/chat/completions"

type Client interface {
	CompleteJSON(ctx context.Context, req CompletionRequest) (string, error)
}

type CompletionRequest struct {
	Model        string
	SystemPrompt string
	UserPrompt   string
	Timeout      time.Duration
}

type HTTPClient struct {
	apiKey       string
	defaultModel string
	baseURL      string
	httpClient   *http.Client
}

func NewHTTPClient(apiKey string, model string) *HTTPClient {
	if model == "" {
		model = "gpt-4o-mini"
	}
	baseURL := strings.TrimSpace(os.Getenv("OPENAI_BASE_URL"))
	if baseURL == "" {
		baseURL = defaultBaseURL
	}
	return &HTTPClient{
		apiKey:       apiKey,
		defaultModel: model,
		baseURL:      baseURL,
		httpClient:   &http.Client{},
	}
}

type chatCompletionRequest struct {
	Model          string         `json:"model"`
	Messages       []chatMessage  `json:"messages"`
	Temperature    float64        `json:"temperature"`
	ResponseFormat map[string]any `json:"response_format,omitempty"`
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatCompletionResponse struct {
	Choices []struct {
		Message chatMessage `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

func (c *HTTPClient) CompleteJSON(ctx context.Context, req CompletionRequest) (string, error) {
	if c.apiKey == "" {
		return "", fmt.Errorf("OPENAI_API_KEY is required")
	}

	model := req.Model
	if model == "" {
		model = c.defaultModel
	}
	timeout := req.Timeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}

	reqCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	payload := chatCompletionRequest{
		Model: model,
		Messages: []chatMessage{
			{Role: "system", Content: req.SystemPrompt},
			{Role: "user", Content: req.UserPrompt},
		},
		Temperature:    0,
		ResponseFormat: map[string]any{"type": "json_object"},
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}

	httpReq, err := http.NewRequestWithContext(reqCtx, http.MethodPost, c.baseURL, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	var parsed chatCompletionResponse
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		return "", fmt.Errorf("unable to parse openai response: %w", err)
	}

	if resp.StatusCode >= 400 {
		if parsed.Error != nil && parsed.Error.Message != "" {
			return "", fmt.Errorf("openai request failed: %s", parsed.Error.Message)
		}
		return "", fmt.Errorf("openai request failed with status %d", resp.StatusCode)
	}

	if len(parsed.Choices) == 0 {
		return "", fmt.Errorf("openai returned zero choices")
	}

	content := strings.TrimSpace(parsed.Choices[0].Message.Content)
	if content == "" {
		return "", fmt.Errorf("openai returned empty content")
	}
	return content, nil
}
