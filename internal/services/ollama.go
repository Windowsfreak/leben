package services

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/windowsfreak/leben/internal/config"
)

type OllamaService struct {
	cfg *config.Config
	client *http.Client
}

func NewOllamaService(cfg *config.Config) *OllamaService {
	return &OllamaService{
		cfg: cfg,
		client: &http.Client{
			Timeout: 15 * time.Second,
		},
	}
}

type ollamaEmbedRequest struct {
	Model string `json:"model"`
	Input string `json:"input"`
}

type ollamaEmbedResponse struct {
	Embeddings [][]float64 `json:"embeddings"`
	Error      string      `json:"error,omitempty"`
}

func (s *OllamaService) GetEmbedding(ctx context.Context, text string, embedType string) ([]float64, error) {
	prefix := s.cfg.Embedding.DocPrefix
	if embedType == "query" {
		prefix = s.cfg.Embedding.QueryPrefix
	}
	inputText := prefix + text

	reqBody, err := json.Marshal(ollamaEmbedRequest{
		Model: s.cfg.Embedding.Model,
		Input: inputText,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to marshal ollama request: %w", err)
	}

	url := fmt.Sprintf("%s/api/embed", s.cfg.Embedding.URL)
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer(reqBody))
	if err != nil {
		return nil, fmt.Errorf("failed to create ollama request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("ollama HTTP request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read ollama response body: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("ollama returned status %d: %s", resp.StatusCode, string(body))
	}

	var res ollamaEmbedResponse
	if err := json.Unmarshal(body, &res); err != nil {
		return nil, fmt.Errorf("failed to unmarshal ollama response: %w", err)
	}

	if res.Error != "" {
		return nil, fmt.Errorf("ollama API error: %s", res.Error)
	}

	if len(res.Embeddings) == 0 {
		return nil, fmt.Errorf("ollama returned empty embeddings")
	}

	return res.Embeddings[0], nil
}


