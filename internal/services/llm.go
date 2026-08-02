package services

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/windowsfreak/leben/internal/config"
)

type LLMService struct {
	cfg    *config.Config
	client *http.Client
}

func NewLLMService(cfg *config.Config) *LLMService {
	return &LLMService{
		cfg: cfg,
		client: &http.Client{
			Timeout: 120 * time.Second,
		},
	}
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatCompletionRequest struct {
	Model       string        `json:"model"`
	Messages    []chatMessage `json:"messages"`
	Temperature float64       `json:"temperature"`
}

type chatCompletionResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
	Error any `json:"error,omitempty"`
}

func (s *LLMService) CallLLM(ctx context.Context, systemPrompt, userContent string) (string, error) {
	url := s.cfg.LLM.URL
	if !strings.Contains(url, "/chat/completions") && !strings.Contains(url, "/completions") {
		url = strings.TrimSuffix(url, "/") + "/v1/chat/completions"
	}

	reqData := chatCompletionRequest{
		Model: s.cfg.LLM.Model,
		Messages: []chatMessage{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: userContent},
		},
		Temperature: 0.3,
	}

	reqBytes, err := json.Marshal(reqData)
	if err != nil {
		return "", fmt.Errorf("failed to marshal LLM request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer(reqBytes))
	if err != nil {
		return "", fmt.Errorf("failed to create LLM request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	if s.cfg.LLM.Pass != "" {
		if s.cfg.LLM.User != "" {
			authVal := base64.StdEncoding.EncodeToString([]byte(s.cfg.LLM.User + ":" + s.cfg.LLM.Pass))
			req.Header.Set("Authorization", "Basic "+authVal)
		} else {
			req.Header.Set("Authorization", "Bearer "+s.cfg.LLM.Pass)
		}
	}

	resp, err := s.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("LLM request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read LLM response body: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("LLM endpoint status %d: %s", resp.StatusCode, string(body))
	}

	var res chatCompletionResponse
	if err := json.Unmarshal(body, &res); err != nil {
		return "", fmt.Errorf("failed to parse LLM response JSON: %w", err)
	}

	if len(res.Choices) == 0 {
		return "", fmt.Errorf("LLM response contained no choices: %s", string(body))
	}

	content := strings.TrimSpace(res.Choices[0].Message.Content)
	return content, nil
}
