package router

import (
	"testing"

	"github.com/windowsfreak/leben/internal/config"
)

func TestRouterInitialization(t *testing.T) {
	cfg := &config.Config{
		Server: config.ServerConfig{WebDir: "."},
	}
	r := New(cfg, nil, nil, nil, nil, nil, nil)
	if r == nil {
		t.Fatal("expected non-nil router")
	}
}
