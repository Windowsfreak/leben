package services

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/windowsfreak/leben/internal/config"
)

func mockEmbedServer(handler http.HandlerFunc) *httptest.Server {
	return httptest.NewServer(handler)
}

func successHandler(vec []float64, delay time.Duration, callCount *atomic.Int64) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if callCount != nil {
			callCount.Add(1)
		}
		if delay > 0 {
			time.Sleep(delay)
		}
		resp := ollamaEmbedResponse{
			Embeddings: [][]float64{vec},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}
}

func errorHandler(statusCode int, callCount *atomic.Int64) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if callCount != nil {
			callCount.Add(1)
		}
		http.Error(w, "server error", statusCode)
	}
}

func TestEmbedding_CloudFastWin(t *testing.T) {
	var cloudCalls, ollamaCalls atomic.Int64
	cloudServer := mockEmbedServer(successHandler([]float64{1.0, 2.0}, 20*time.Millisecond, &cloudCalls))
	defer cloudServer.Close()
	ollamaServer := mockEmbedServer(successHandler([]float64{9.0, 9.0}, 0, &ollamaCalls))
	defer ollamaServer.Close()

	cfg := &config.Config{
		Embedding: config.EmbeddingConfig{
			URL:         cloudServer.URL,
			FallbackURL: ollamaServer.URL,
			Model:       "nomic-embed-text-v2-moe",
			Token:       "test-token",
		},
	}

	svc := NewOllamaService(cfg)
	vec, err := svc.GetEmbedding(context.Background(), "test text", "query")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(vec) != 2 || vec[0] != 1.0 {
		t.Errorf("expected cloud vector [1.0, 2.0], got %v", vec)
	}
	if cloudCalls.Load() != 1 {
		t.Errorf("expected 1 cloud call, got %d", cloudCalls.Load())
	}
	// Give a moment to confirm ollama was never called
	time.Sleep(50 * time.Millisecond)
	if ollamaCalls.Load() != 0 {
		t.Errorf("expected 0 ollama calls when cloud is fast, got %d", ollamaCalls.Load())
	}
}

func TestEmbedding_CloudSlow_OllamaWins(t *testing.T) {
	var cloudCalls, ollamaCalls atomic.Int64
	// Cloud takes 1.5s (exceeds 1s hedge delay)
	cloudServer := mockEmbedServer(successHandler([]float64{1.0, 2.0}, 1500*time.Millisecond, &cloudCalls))
	defer cloudServer.Close()
	// Ollama responds in 50ms once hedge timer triggers at 1.0s
	ollamaServer := mockEmbedServer(successHandler([]float64{9.0, 9.0}, 50*time.Millisecond, &ollamaCalls))
	defer ollamaServer.Close()

	cfg := &config.Config{
		Embedding: config.EmbeddingConfig{
			URL:         cloudServer.URL,
			FallbackURL: ollamaServer.URL,
			Model:       "nomic-embed-text-v2-moe",
		},
	}

	svc := NewOllamaService(cfg)
	start := time.Now()
	vec, err := svc.GetEmbedding(context.Background(), "test text", "query")
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Ollama should have won at ~1.05s
	if len(vec) != 2 || vec[0] != 9.0 {
		t.Errorf("expected ollama vector [9.0, 9.0], got %v", vec)
	}
	if elapsed > 1300*time.Millisecond {
		t.Errorf("expected request to finish around 1.05s, took %v", elapsed)
	}
	if ollamaCalls.Load() != 1 {
		t.Errorf("expected 1 ollama call, got %d", ollamaCalls.Load())
	}
}

func TestEmbedding_CloudHardError_ImmediateFallback(t *testing.T) {
	var cloudCalls, ollamaCalls atomic.Int64
	// Cloud returns 500 immediately
	cloudServer := mockEmbedServer(errorHandler(http.StatusInternalServerError, &cloudCalls))
	defer cloudServer.Close()
	ollamaServer := mockEmbedServer(successHandler([]float64{9.0, 9.0}, 10*time.Millisecond, &ollamaCalls))
	defer ollamaServer.Close()

	cfg := &config.Config{
		Embedding: config.EmbeddingConfig{
			URL:         cloudServer.URL,
			FallbackURL: ollamaServer.URL,
			Model:       "nomic-embed-text-v2-moe",
		},
	}

	svc := NewOllamaService(cfg)
	start := time.Now()
	vec, err := svc.GetEmbedding(context.Background(), "test text", "query")
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Ollama should be triggered immediately without waiting 1 second
	if elapsed > 500*time.Millisecond {
		t.Errorf("expected immediate fallback (<500ms), took %v", elapsed)
	}
	if vec[0] != 9.0 {
		t.Errorf("expected ollama vector, got %v", vec)
	}

	// Check that 10-minute bypass was activated
	if svc.hardErrorUntil.Load() <= time.Now().UnixNano() {
		t.Errorf("expected hardErrorUntil to be set in the future")
	}

	// Next request should skip Cloud completely
	cloudCallsBefore := cloudCalls.Load()
	_, _ = svc.GetEmbedding(context.Background(), "second query", "query")
	if cloudCalls.Load() != cloudCallsBefore {
		t.Errorf("expected Cloud to be bypassed during 10m error window, got extra call")
	}
}

func TestEmbedding_FastParallelTriggerAndRecovery(t *testing.T) {
	cfg := &config.Config{
		Embedding: config.EmbeddingConfig{
			URL:         "http://cloud.mock",
			FallbackURL: "http://ollama.mock",
			Model:       "nomic-embed-text-v2-moe",
		},
	}
	svc := NewOllamaService(cfg)

	// Simulate first slow request at t=0
	t0 := time.Now().Add(-6 * time.Second).UnixNano()
	svc.consecutiveSlow.Store(1)
	svc.firstSlowAt.Store(t0)

	// Second slow request at now (delta >= 5s)
	svc.recordCloudSlow()

	// Should have entered Fast-Parallel mode
	if svc.parallelModeUntil.Load() <= time.Now().UnixNano() {
		t.Fatalf("expected parallelModeUntil to be active")
	}

	// When Cloud wins a race, breaker should immediately close
	svc.recordCloudFastWin()
	if svc.parallelModeUntil.Load() != 0 {
		t.Errorf("expected parallelModeUntil to be reset to 0 upon Cloud fast win")
	}
	if svc.consecutiveSlow.Load() != 0 {
		t.Errorf("expected consecutiveSlow to be reset to 0")
	}
}
