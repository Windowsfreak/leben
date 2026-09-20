package services

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/windowsfreak/leben/internal/config"
)

type OllamaService struct {
	cfg    *config.Config
	client *http.Client
}

func NewOllamaService(cfg *config.Config) *OllamaService {
	return &OllamaService{
		cfg: cfg,
		client: &http.Client{
			Timeout: 20 * time.Second,
		},
	}
}

type ollamaEmbedRequest struct {
	Model string `json:"model,omitempty"`
	Input any    `json:"input"`
}

type ollamaEmbedResponse struct {
	Embedding  []float64   `json:"embedding,omitempty"`
	Embeddings [][]float64 `json:"embeddings,omitempty"`
	Dim        int         `json:"dim,omitempty"`
	Error      string      `json:"error,omitempty"`
}

func decodeBinaryFloats(body []byte, expectedDim int) ([][]float64, error) {
	if expectedDim <= 0 {
		expectedDim = 384
	}
	bytesPerVec := expectedDim * 4
	if len(body)%bytesPerVec != 0 {
		return nil, fmt.Errorf("invalid binary float buffer size: %d bytes (expected multiple of %d)", len(body), bytesPerVec)
	}
	count := len(body) / bytesPerVec
	vectors := make([][]float64, count)
	offset := 0
	for i := 0; i < count; i++ {
		vec := make([]float64, expectedDim)
		for j := 0; j < expectedDim; j++ {
			bits := binary.LittleEndian.Uint32(body[offset : offset+4])
			vec[j] = float64(math.Float32frombits(bits))
			offset += 4
		}
		vectors[i] = vec
	}
	return vectors, nil
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
		return nil, fmt.Errorf("failed to marshal embed request: %w", err)
	}

	url := fmt.Sprintf("%s/api/embed", s.cfg.Embedding.URL)
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer(reqBody))
	if err != nil {
		return nil, fmt.Errorf("failed to create embed request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/octet-stream, application/json")

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("embed HTTP request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read embed response body: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("embed returned status %d: %s", resp.StatusCode, string(body))
	}

	// 1. Binary payload path (application/octet-stream)
	if strings.Contains(resp.Header.Get("Content-Type"), "application/octet-stream") {
		dim := 384
		if dimHeader := resp.Header.Get("X-Embedding-Dim"); dimHeader != "" {
			if d, err := strconv.Atoi(dimHeader); err == nil && d > 0 {
				dim = d
			}
		}
		vecs, err := decodeBinaryFloats(body, dim)
		if err != nil {
			return nil, fmt.Errorf("failed to decode binary floats: %w", err)
		}
		if len(vecs) == 0 {
			return nil, fmt.Errorf("empty binary embeddings returned")
		}
		return vecs[0], nil
	}

	// 2. JSON payload path
	var res ollamaEmbedResponse
	if err := json.Unmarshal(body, &res); err != nil {
		return nil, fmt.Errorf("failed to unmarshal embed response: %w", err)
	}

	if res.Error != "" {
		return nil, fmt.Errorf("embed API error: %s", res.Error)
	}

	if len(res.Embedding) > 0 {
		return res.Embedding, nil
	}
	if len(res.Embeddings) > 0 {
		return res.Embeddings[0], nil
	}

	return nil, fmt.Errorf("embed returned empty embeddings")
}

func (s *OllamaService) GetEmbeddings(ctx context.Context, texts []string) ([][]float64, error) {
	if len(texts) == 0 {
		return nil, nil
	}

	reqBody, err := json.Marshal(ollamaEmbedRequest{
		Model: s.cfg.Embedding.Model,
		Input: texts,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to marshal batch embed request: %w", err)
	}

	url := fmt.Sprintf("%s/api/embed", s.cfg.Embedding.URL)
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer(reqBody))
	if err != nil {
		return nil, fmt.Errorf("failed to create batch embed request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/octet-stream, application/json")

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("batch embed HTTP request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read batch embed response body: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("batch embed returned status %d: %s", resp.StatusCode, string(body))
	}

	// 1. Binary payload path (application/octet-stream)
	if strings.Contains(resp.Header.Get("Content-Type"), "application/octet-stream") {
		dim := 384
		if dimHeader := resp.Header.Get("X-Embedding-Dim"); dimHeader != "" {
			if d, err := strconv.Atoi(dimHeader); err == nil && d > 0 {
				dim = d
			}
		}
		return decodeBinaryFloats(body, dim)
	}

	// 2. JSON payload path
	var res ollamaEmbedResponse
	if err := json.Unmarshal(body, &res); err != nil {
		return nil, fmt.Errorf("failed to unmarshal batch embed response: %w", err)
	}

	if res.Error != "" {
		return nil, fmt.Errorf("batch embed API error: %s", res.Error)
	}

	if len(res.Embeddings) > 0 {
		return res.Embeddings, nil
	}

	return nil, fmt.Errorf("batch embed returned empty embeddings")
}
