package services

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/windowsfreak/leben/internal/config"
)

type OllamaService struct {
	cfg    *config.Config
	client *http.Client

	hardErrorUntil    atomic.Int64 // UnixNano: skip Cloud until this timestamp (10m cooldown on hard error)
	parallelModeUntil atomic.Int64 // UnixNano: run both Cloud and Ollama in parallel until this timestamp (5m window)
	consecutiveSlow   atomic.Int64 // Count of consecutive slow requests (>1s)
	firstSlowAt       atomic.Int64 // UnixNano: timestamp of the first slow request in the current sequence
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
	Model string `json:"model"`
	Input string `json:"input"`
}

type ollamaEmbedResponse struct {
	Embeddings [][]float64 `json:"embeddings"`
	Error      string      `json:"error,omitempty"`
}

func (s *OllamaService) fallbackModel() string {
	if s.cfg.Embedding.FallbackModel != "" {
		return s.cfg.Embedding.FallbackModel
	}
	return s.cfg.Embedding.Model
}

func (s *OllamaService) recordHardError() {
	now := time.Now().UnixNano()
	s.hardErrorUntil.Store(now + int64(10*time.Minute))
	log.Printf("[Embedding] Cloud GPU reported hard error; bypassing Cloud for 10m (until %s)",
		time.Unix(0, s.hardErrorUntil.Load()).Format("15:04:05"))
}

func (s *OllamaService) recordCloudSlow() {
	now := time.Now().UnixNano()
	if s.consecutiveSlow.CompareAndSwap(0, 1) {
		s.firstSlowAt.Store(now)
		return
	}
	count := s.consecutiveSlow.Add(1)
	first := s.firstSlowAt.Load()
	if count >= 2 && time.Duration(now-first) >= 5*time.Second {
		s.parallelModeUntil.Store(now + int64(5*time.Minute))
		s.consecutiveSlow.Store(0)
		s.firstSlowAt.Store(0)
		log.Printf("[Embedding] Cloud GPU latency exceeded 1s across %v (%d slow events); entered Fast-Parallel mode for 5m",
			time.Duration(now-first).Round(time.Millisecond), count)
	}
}

func (s *OllamaService) recordCloudFastWin() {
	if s.parallelModeUntil.Load() != 0 || s.consecutiveSlow.Load() != 0 {
		s.parallelModeUntil.Store(0)
		s.consecutiveSlow.Store(0)
		s.firstSlowAt.Store(0)
		log.Printf("[Embedding] Cloud GPU won the race; circuit breaker closed, restored normal 1s hedge delay")
	}
}

func (s *OllamaService) queryEndpoint(ctx context.Context, endpointURL, token, model, text, embedType string) ([]float64, error) {
	prefix := s.cfg.Embedding.DocPrefix
	if embedType == "query" {
		prefix = s.cfg.Embedding.QueryPrefix
	}
	inputText := prefix + text

	reqBody, err := json.Marshal(ollamaEmbedRequest{
		Model: model,
		Input: inputText,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to marshal embed request: %w", err)
	}

	url := fmt.Sprintf("%s/api/embed", endpointURL)
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer(reqBody))
	if err != nil {
		return nil, fmt.Errorf("failed to create embed request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

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
		return nil, fmt.Errorf("embed service returned status %d: %s", resp.StatusCode, string(body))
	}

	var res ollamaEmbedResponse
	if err := json.Unmarshal(body, &res); err != nil {
		return nil, fmt.Errorf("failed to unmarshal embed response: %w", err)
	}

	if res.Error != "" {
		return nil, fmt.Errorf("embed API error: %s", res.Error)
	}

	if len(res.Embeddings) == 0 {
		return nil, fmt.Errorf("embed service returned empty embeddings")
	}

	return res.Embeddings[0], nil
}

func (s *OllamaService) GetEmbedding(ctx context.Context, text string, embedType string) ([]float64, error) {
	// If fallback is not configured, query primary directly
	if s.cfg.Embedding.FallbackURL == "" {
		return s.queryEndpoint(ctx, s.cfg.Embedding.URL, s.cfg.Embedding.Token, s.cfg.Embedding.Model, text, embedType)
	}

	now := time.Now().UnixNano()

	// 1. If Cloud GPU is in 10-minute hard-error cooldown, skip directly to Ollama
	if now < s.hardErrorUntil.Load() {
		return s.queryEndpoint(ctx, s.cfg.Embedding.FallbackURL, "", s.fallbackModel(), text, embedType)
	}

	// 2. Determine hedging delay (0 if in parallel mode, else 1 second)
	hedgeDelay := 1 * time.Second
	if now < s.parallelModeUntil.Load() {
		hedgeDelay = 0
	}

	cloudCtx, cancelCloud := context.WithCancel(ctx)
	defer cancelCloud()

	ollamaCtx, cancelOllama := context.WithCancel(ctx)
	defer cancelOllama()

	type result struct {
		vec    []float64
		err    error
		source string // "cloud" or "ollama"
	}

	resCh := make(chan result, 2)
	triggerFallbackCh := make(chan struct{}, 1)
	doneCh := make(chan struct{})
	var closeOnce sync.Once
	markDone := func() {
		closeOnce.Do(func() {
			close(doneCh)
		})
	}
	defer markDone()

	// Launch Cloud GPU
	go func() {
		t0 := time.Now()
		vec, err := s.queryEndpoint(cloudCtx, s.cfg.Embedding.URL, s.cfg.Embedding.Token, s.cfg.Embedding.Model, text, embedType)
		dur := time.Since(t0)

		if err != nil {
			if cloudCtx.Err() == nil {
				s.recordHardError()
				// Trigger fallback immediately without waiting for hedgeDelay
				select {
				case triggerFallbackCh <- struct{}{}:
				default:
				}
			}
			resCh <- result{err: err, source: "cloud"}
			return
		}

		if dur <= 1*time.Second {
			s.recordCloudFastWin()
		}
		resCh <- result{vec: vec, source: "cloud"}
	}()

	// Launch Ollama with hedgeDelay
	go func() {
		if hedgeDelay > 0 {
			timer := time.NewTimer(hedgeDelay)
			defer timer.Stop()

			select {
			case <-timer.C:
				s.recordCloudSlow()
			case <-triggerFallbackCh:
				// Cloud GPU failed early, start fallback immediately
			case <-doneCh:
				return // Cloud GPU won before hedgeDelay expired! Ollama never called
			case <-ctx.Done():
				return
			}
		}

		vec, err := s.queryEndpoint(ollamaCtx, s.cfg.Embedding.FallbackURL, "", s.fallbackModel(), text, embedType)
		resCh <- result{vec: vec, err: err, source: "ollama"}
	}()

	// Wait for the first success, or collect errors if both fail
	var firstErr error
	for i := 0; i < 2; i++ {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case res := <-resCh:
			if res.err == nil {
				markDone()
				if res.source == "cloud" {
					cancelOllama()
					s.recordCloudFastWin()
				} else {
					cancelCloud()
				}
				return res.vec, nil
			}
			if firstErr == nil {
				firstErr = res.err
			}
		}
	}

	return nil, fmt.Errorf("both primary and fallback embedding services failed: %w", firstErr)
}
