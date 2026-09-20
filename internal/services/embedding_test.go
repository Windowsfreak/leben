package services

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/windowsfreak/leben/internal/config"
)

func TestEmbeddingBinaryDecoding(t *testing.T) {
	// Create dummy server returning binary float32 stream
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Accept") == "" {
			t.Errorf("Expected Accept header to be set")
		}

		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("X-Embedding-Dim", "384")
		w.Header().Set("X-Batch-Size", "1")

		// Write 384 float32s
		buf := make([]byte, 384*4)
		for i := 0; i < 384; i++ {
			val := float32(i) * 0.1
			binary.LittleEndian.PutUint32(buf[i*4:], math.Float32bits(val))
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(buf)
	}))
	defer ts.Close()

	cfg := &config.Config{}
	cfg.Embedding.URL = ts.URL
	cfg.Embedding.Model = "ibm-granite/granite-embedding-97m-multilingual-r2"

	svc := NewOllamaService(cfg)
	vec, err := svc.GetEmbedding(context.Background(), "test text", "document")
	if err != nil {
		t.Fatalf("GetEmbedding failed: %v", err)
	}

	if len(vec) != 384 {
		t.Fatalf("Expected 384 elements, got %d", len(vec))
	}

	for i := 0; i < 384; i++ {
		expected := float64(float32(i) * 0.1)
		if math.Abs(vec[i]-expected) > 1e-4 {
			t.Errorf("Index %d: expected %f, got %f", i, expected, vec[i])
			break
		}
	}
}

func TestBatchEmbeddingBinaryDecoding(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("X-Embedding-Dim", "384")
		w.Header().Set("X-Batch-Size", "2")

		// 2 vectors of 384 float32s = 768 float32s
		buf := make([]byte, 2*384*4)
		for i := 0; i < 2*384; i++ {
			val := float32(i) * 0.05
			binary.LittleEndian.PutUint32(buf[i*4:], math.Float32bits(val))
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(buf)
	}))
	defer ts.Close()

	cfg := &config.Config{}
	cfg.Embedding.URL = ts.URL
	cfg.Embedding.Model = "ibm-granite/granite-embedding-97m-multilingual-r2"

	svc := NewOllamaService(cfg)
	vecs, err := svc.GetEmbeddings(context.Background(), []string{"hello", "world"})
	if err != nil {
		t.Fatalf("GetEmbeddings failed: %v", err)
	}

	if len(vecs) != 2 {
		t.Fatalf("Expected 2 vectors, got %d", len(vecs))
	}
	if len(vecs[0]) != 384 || len(vecs[1]) != 384 {
		t.Fatalf("Expected each vector to have 384 dims, got %d and %d", len(vecs[0]), len(vecs[1]))
	}
}

func TestEmbeddingJSONFallback(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		resp := ollamaEmbedResponse{
			Embedding: make([]float64, 384),
			Dim:       384,
		}
		resp.Embedding[0] = 0.42
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer ts.Close()

	cfg := &config.Config{}
	cfg.Embedding.URL = ts.URL
	cfg.Embedding.Model = "ibm-granite/granite-embedding-97m-multilingual-r2"

	svc := NewOllamaService(cfg)
	vec, err := svc.GetEmbedding(context.Background(), "fallback json", "query")
	if err != nil {
		t.Fatalf("GetEmbedding failed: %v", err)
	}

	if len(vec) != 384 {
		t.Fatalf("Expected 384 elements, got %d", len(vec))
	}
	if math.Abs(vec[0]-0.42) > 1e-4 {
		t.Errorf("Expected vec[0] == 0.42, got %f", vec[0])
	}
}
