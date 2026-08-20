package services

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/windowsfreak/leben/internal/config"
)

func TestNormalizeDeepLLangCode(t *testing.T) {
	if code := NormalizeDeepLLangCode("de", true); code != "DE" {
		t.Errorf("expected DE, got %s", code)
	}
	if code := NormalizeDeepLLangCode("en", true); code != "EN-US" {
		t.Errorf("expected EN-US, got %s", code)
	}
	if code := NormalizeDeepLLangCode("en", false); code != "EN" {
		t.Errorf("expected EN, got %s", code)
	}
}

func TestDeepLServiceMock(t *testing.T) {
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "DeepL-Auth-Key test-key" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{
			"translations": [
				{"detected_source_language": "DE", "text": "Hello World"}
			]
		}`))
	}))
	defer mockServer.Close()

	cfg := &config.Config{
		DeepL: config.DeepLConfig{
			APIKey: "test-key",
			URL:    mockServer.URL,
		},
	}

	svc := NewDeepLService(cfg)
	if !svc.IsConfigured() {
		t.Fatal("expected svc to be configured")
	}

	res, err := svc.TranslateSingle(context.Background(), "Hallo Welt", "de", "en")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if res != "Hello World" {
		t.Fatalf("expected 'Hello World', got %s", res)
	}
}

func TestDeepLLiveAPI(t *testing.T) {
	t.Skip("skipping live DeepL test to save token/character quota")
}

func TestDeepLLiveMaskedHTMLTranslation(t *testing.T) {
	t.Skip("skipping live DeepL masked HTML test to save token/character quota")
}
