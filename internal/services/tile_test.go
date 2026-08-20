package services

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadSupportedLanguages(t *testing.T) {
	// 1. Test with real frontend directory
	langs := LoadSupportedLanguages("../../frontend")
	if len(langs) == 0 {
		t.Fatalf("expected supported languages from frontend/config.json, got empty map")
	}
	if langs["de"] != "Deutsch" {
		t.Errorf("expected de: Deutsch, got %s", langs["de"])
	}
	if langs["en"] != "English" {
		t.Errorf("expected en: English, got %s", langs["en"])
	}
	if langs["fr"] != "Français" {
		t.Errorf("expected fr: Français, got %s", langs["fr"])
	}
	if langs["ar"] != "العربية" {
		t.Errorf("expected ar: العربية, got %s", langs["ar"])
	}

	// 2. Test fallback on non-existent directory
	fallback := LoadSupportedLanguages("/path/does/not/exist")
	if len(fallback) != 2 || fallback["de"] != "Deutsch" || fallback["en"] != "English" {
		t.Errorf("expected default fallback map, got %+v", fallback)
	}

	// 3. Test dynamic file reload
	tmpDir, err := os.MkdirTemp("", "leben-lang-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	testJSON := `{"supported_languages": {"de": "Deutsch", "ja": "日本語"}}`
	if err := os.WriteFile(filepath.Join(tmpDir, "config.json"), []byte(testJSON), 0644); err != nil {
		t.Fatalf("failed to write test config.json: %v", err)
	}

	customLangs := LoadSupportedLanguages(tmpDir)
	if len(customLangs) != 2 || customLangs["ja"] != "日本語" {
		t.Errorf("expected custom languages, got %+v", customLangs)
	}
}
