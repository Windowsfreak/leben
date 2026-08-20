package services

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/windowsfreak/leben/internal/config"
)

type DeepLService struct {
	cfg    *config.Config
	client *http.Client
}

func NewDeepLService(cfg *config.Config) *DeepLService {
	return &DeepLService{
		cfg: cfg,
		client: &http.Client{
			Timeout: 60 * time.Second,
		},
	}
}

func (s *DeepLService) IsConfigured() bool {
	return s.cfg != nil && strings.TrimSpace(s.cfg.DeepL.APIKey) != ""
}

type deepLRequest struct {
	Text       []string `json:"text"`
	SourceLang string   `json:"source_lang,omitempty"`
	TargetLang string   `json:"target_lang"`
}

type deepLResponse struct {
	Translations []struct {
		DetectedSourceLanguage string `json:"detected_source_language"`
		Text                   string `json:"text"`
	} `json:"translations"`
	Message string `json:"message,omitempty"`
}

// NormalizeLanguageCode maps standard ISO codes ("de", "en") to DeepL required codes ("DE", "EN-US")
func NormalizeDeepLLangCode(lang string, isTarget bool) string {
	clean := strings.ToUpper(strings.TrimSpace(lang))
	if isTarget {
		if clean == "EN" {
			return "EN-US"
		}
	}
	return clean
}

func (s *DeepLService) Translate(ctx context.Context, texts []string, sourceLang, targetLang string) ([]string, error) {
	if !s.IsConfigured() {
		return nil, fmt.Errorf("DeepL API key is not configured")
	}

	if len(texts) == 0 {
		return nil, nil
	}

	targetCode := NormalizeDeepLLangCode(targetLang, true)
	sourceCode := NormalizeDeepLLangCode(sourceLang, false)

	reqPayload := deepLRequest{
		Text:       texts,
		TargetLang: targetCode,
	}
	if sourceCode != "" {
		reqPayload.SourceLang = sourceCode
	}

	reqBody, err := json.Marshal(reqPayload)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal DeepL request: %w", err)
	}

	apiURL := s.cfg.DeepL.URL
	if strings.TrimSpace(apiURL) == "" {
		if strings.HasSuffix(s.cfg.DeepL.APIKey, ":fx") {
			apiURL = "https://api-free.deepl.com/v2/translate"
		} else {
			apiURL = "https://api.deepl.com/v2/translate"
		}
	}

	req, err := http.NewRequestWithContext(ctx, "POST", apiURL, bytes.NewBuffer(reqBody))
	if err != nil {
		return nil, fmt.Errorf("failed to create DeepL HTTP request: %w", err)
	}

	req.Header.Set("Authorization", "DeepL-Auth-Key "+strings.TrimSpace(s.cfg.DeepL.APIKey))
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("DeepL HTTP request failed: %w", err)
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read DeepL response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("DeepL API error (status %d): %s", resp.StatusCode, string(bodyBytes))
	}

	var res deepLResponse
	if err := json.Unmarshal(bodyBytes, &res); err != nil {
		return nil, fmt.Errorf("failed to parse DeepL response JSON: %w", err)
	}

	if len(res.Translations) != len(texts) {
		return nil, fmt.Errorf("DeepL returned %d translations, expected %d", len(res.Translations), len(texts))
	}

	results := make([]string, len(res.Translations))
	for i, t := range res.Translations {
		results[i] = t.Text
	}

	return results, nil
}

func (s *DeepLService) TranslateSingle(ctx context.Context, text, sourceLang, targetLang string) (string, error) {
	results, err := s.Translate(ctx, []string{text}, sourceLang, targetLang)
	if err != nil {
		return "", err
	}
	if len(results) == 0 {
		return "", fmt.Errorf("empty translation response from DeepL")
	}
	return results[0], nil
}
