package services

import (
	"context"
	"testing"
	"time"

	"github.com/windowsfreak/leben/internal/config"
)

func TestEmbedding_LiveCloudEndpoint(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping live cloud embedding test in short mode")
	}

	cfg, err := config.Load("../../config.yml")
	if err != nil {
		t.Skip("skipping live test: config.yml not found")
	}

	if cfg.Embedding.Token == "" {
		t.Skip("skipping live test: no token configured")
	}

	svc := NewOllamaService(cfg)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	vec, err := svc.GetEmbedding(ctx, "financial independence", "query")
	if err != nil {
		t.Fatalf("Live Cloud embedding request failed: %v", err)
	}

	if len(vec) != 768 {
		t.Fatalf("expected 768 dimensions from live Cloud service, got %d", len(vec))
	}

	t.Logf("Successfully received 768-dim embedding from live Cloud GPU endpoint! Sample: %v", vec[:5])
}
