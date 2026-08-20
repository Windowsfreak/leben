package services

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/windowsfreak/leben/internal/config"
	"github.com/windowsfreak/leben/internal/db"
	"github.com/windowsfreak/leben/internal/models"
	"github.com/windowsfreak/leben/internal/tasks"
)

type TranslationService struct {
	cfg        *config.Config
	database   *db.DB
	tileSvc    *TileService
	llmSvc     *LLMService
	ollamaSvc  *OllamaService
	deepLSvc   *DeepLService
	taskMgr    *tasks.Manager
}

func NewTranslationService(
	cfg *config.Config,
	database *db.DB,
	tileSvc *TileService,
	llmSvc *LLMService,
	ollamaSvc *OllamaService,
	taskMgr *tasks.Manager,
) *TranslationService {
	return &TranslationService{
		cfg:       cfg,
		database:  database,
		tileSvc:   tileSvc,
		llmSvc:    llmSvc,
		ollamaSvc: ollamaSvc,
		deepLSvc:  NewDeepLService(cfg),
		taskMgr:   taskMgr,
	}
}

type translatedMetadata struct {
	Title   string `json:"title"`
	Summary string `json:"summary"`
	Tags    any    `json:"tags"`
}

func (s *TranslationService) StartAutoTranslateTask(parentCtx context.Context, name, targetLang string) (*models.TranslationTask, error) {
	task, taskCtx, cleanup, err := s.taskMgr.CreateTask(parentCtx, name, targetLang)
	if err != nil {
		return nil, err
	}

	go func() {
		defer cleanup()
		s.runTranslateTask(taskCtx, task.ID, name, targetLang)
	}()

	return task, nil
}

func (s *TranslationService) runTranslateTask(ctx context.Context, taskID, name, targetLang string) {
	s.taskMgr.UpdateTaskProgress(taskID, fmt.Sprintf("Fetching source tile '%s'...", name))

	sourceTile, err := s.tileSvc.GetTile(ctx, name, "de", nil, true)
	if err != nil {
		s.taskMgr.FailTask(taskID, fmt.Errorf("source tile '%s' not found: %w", name, err))
		return
	}

	supportedMap := map[string]string{
		"de": "Deutsch",
		"en": "English",
	}

	var targets []string
	if targetLang == "all" || targetLang == "missing" {
		statusMap, err := s.tileSvc.GetTranslationStatus(ctx, name)
		if err == nil && statusMap[name] != nil && len(statusMap[name].MissingLanguages) > 0 {
			targets = statusMap[name].MissingLanguages
		} else {
			for code := range supportedMap {
				if code != sourceTile.Language {
					targets = append(targets, code)
				}
			}
		}
	} else {
		targetLang = strings.ToLower(strings.TrimSpace(targetLang))
		if targetLang == sourceTile.Language {
			s.taskMgr.FailTask(taskID, fmt.Errorf("target language '%s' is identical to source language", targetLang))
			return
		}
		targets = []string{targetLang}
	}

	if len(targets) == 0 {
		s.taskMgr.CompleteTask(taskID, map[string]any{
			"message": fmt.Sprintf("Tile '%s' is already up to date for all languages.", name),
		})
		return
	}

	contentsDir := filepath.Join(s.cfg.Server.WebDir, "content")
	var results []map[string]string

	for idx, tLang := range targets {
		select {
		case <-ctx.Done():
			s.taskMgr.UpdateTaskProgress(taskID, "Cancelled by user context.")
			return
		default:
		}

		langFullName := supportedMap[tLang]
		if langFullName == "" {
			langFullName = strings.ToUpper(tLang)
		}

		// 1. Translate Metadata
		s.taskMgr.UpdateTaskProgress(taskID, fmt.Sprintf("[%d/%d] Translating metadata to %s...", idx+1, len(targets), langFullName))
		transMeta, err := s.translateMetadata(ctx, sourceTile, tLang, langFullName, taskID)
		if err != nil {
			s.taskMgr.FailTask(taskID, fmt.Errorf("metadata translation failed for %s: %w", tLang, err))
			return
		}

		// 2. Translate Teaser
		translatedTeaser := ""
		if strings.TrimSpace(sourceTile.HTMLTeaser) != "" {
			s.taskMgr.UpdateTaskProgress(taskID, fmt.Sprintf("[%d/%d] Translating HTML teaser to %s...", idx+1, len(targets), langFullName))
			teaserRes, err := s.translateHTML(ctx, sourceTile.HTMLTeaser, sourceTile.Language, tLang, langFullName, taskID, "teaser")
			if err != nil {
				s.taskMgr.FailTask(taskID, fmt.Errorf("teaser translation failed for %s: %w", tLang, err))
				return
			}
			translatedTeaser = teaserRes
		}

		// 3. Translate content file if exists
		targetContentFile := ""
		if sourceTile.ContentFile != "" {
			s.taskMgr.UpdateTaskProgress(taskID, fmt.Sprintf("[%d/%d] Translating content file to %s...", idx+1, len(targets), langFullName))
			sourceFilePath := filepath.Join(contentsDir, sourceTile.ContentFile)
			if contentBytes, err := os.ReadFile(sourceFilePath); err == nil {
				contentRes, err := s.translateHTML(ctx, string(contentBytes), sourceTile.Language, tLang, langFullName, taskID, "content file")
				if err != nil {
					s.taskMgr.FailTask(taskID, fmt.Errorf("content translation failed for %s: %w", tLang, err))
					return
				}

				baseName := strings.TrimSuffix(sourceTile.ContentFile, filepath.Ext(sourceTile.ContentFile))
				re := regexp.MustCompile(`[_\-][a-zA-Z]{2}$`)
				baseClean := re.ReplaceAllString(baseName, "")
				targetContentFile = fmt.Sprintf("%s_%s.html", baseClean, tLang)

				_ = os.WriteFile(filepath.Join(contentsDir, targetContentFile), []byte(contentRes), 0644)
			}
		}

		tagsStr := ""
		switch t := transMeta.Tags.(type) {
		case string:
			tagsStr = t
		case []any:
			var parts []string
			for _, item := range t {
				if str, ok := item.(string); ok {
					parts = append(parts, str)
				}
			}
			tagsStr = strings.Join(parts, ", ")
		}
		if strings.TrimSpace(tagsStr) == "" {
			tagsStr = sourceTile.Tags
		}
		summary := transMeta.Summary
		if summary == "" {
			summary = sourceTile.Summary
		}

		targetTile := &models.Tile{
			Name:        name,
			Language:    tLang,
			Title:       transMeta.Title,
			HTMLTeaser:  translatedTeaser,
			Summary:     summary,
			Tags:        tagsStr,
			Type:        sourceTile.Type,
			Link:        sourceTile.Link,
			ContentFile: targetContentFile,
			Visible:     sourceTile.Visible,
			Secret:      sourceTile.Secret,
			AccentColor: sourceTile.AccentColor,
			Background:  sourceTile.Background,
			SortOrder:   sourceTile.SortOrder,
		}

		if existing, err := s.tileSvc.GetTile(ctx, name, tLang, nil, true); err == nil && existing != nil {
			targetTile.ID = existing.ID
		}

		s.taskMgr.UpdateTaskProgress(taskID, fmt.Sprintf("[%d/%d] Saving tile '%s' (%s)...", idx+1, len(targets), name, tLang))
		if err := s.tileSvc.SaveTile(ctx, targetTile); err != nil {
			s.taskMgr.FailTask(taskID, fmt.Errorf("failed to save tile '%s' (%s): %w", name, tLang, err))
			return
		}

		results = append(results, map[string]string{
			"language":     tLang,
			"title":        transMeta.Title,
			"content_file": targetContentFile,
		})
	}

	s.taskMgr.CompleteTask(taskID, map[string]any{
		"name":            name,
		"source_language": sourceTile.Language,
		"translated":      results,
	})
}

// translateMetadata translates title, summary, and tags using DeepL if available, with LLM fallback.
func (s *TranslationService) translateMetadata(ctx context.Context, sourceTile *models.Tile, targetLang, targetFullName, taskID string) (*translatedMetadata, error) {
	// Try DeepL first if configured
	if s.deepLSvc != nil && s.deepLSvc.IsConfigured() {
		metaTexts := []string{sourceTile.Title, sourceTile.Summary, sourceTile.Tags}
		translated, err := s.deepLSvc.Translate(ctx, metaTexts, sourceTile.Language, targetLang)
		if err == nil && len(translated) == 3 {
			return &translatedMetadata{
				Title:   strings.TrimSpace(translated[0]),
				Summary: strings.TrimSpace(translated[1]),
				Tags:    strings.TrimSpace(translated[2]),
			}, nil
		}
		// DeepL failed or had error, fallback to LLM
		s.taskMgr.UpdateTaskProgress(taskID, fmt.Sprintf("DeepL metadata translation encountered issue (%v), falling back to LLM...", err))
	}

	// LLM Fallback with Sister-Document prompting
	metaPrompt := fmt.Sprintf(`You are an elite bilingual author and translator. Translate the following tile metadata from %s into %s so that it reads naturally and idiomatically as an authentic sister document.
Respond ONLY with a valid JSON object (no markdown formatting, no backticks):
{
  "title": "Translated Title",
  "summary": "Translated summary...",
  "tags": "tag1, tag2"
}`, strings.ToUpper(sourceTile.Language), targetFullName)

	metaUser := fmt.Sprintf("Source Title: %s\nSource Tags: %s\nSource Summary: %s",
		sourceTile.Title, sourceTile.Tags, sourceTile.Summary)

	metaRes, err := s.llmSvc.CallLLM(ctx, metaPrompt, metaUser)
	if err != nil {
		return nil, fmt.Errorf("LLM metadata translation failed: %w", err)
	}

	cleanJSON := stripBackticks(metaRes)
	var transMeta translatedMetadata
	if err := json.Unmarshal([]byte(cleanJSON), &transMeta); err != nil {
		return nil, fmt.Errorf("invalid metadata JSON from LLM: %w", err)
	}

	return &transMeta, nil
}

// translateHTML translates HTML content using Mustache tag masking + collapsing with DeepL / LLM.
func (s *TranslationService) translateHTML(ctx context.Context, sourceHTML, sourceLang, targetLang, targetFullName, taskID, label string) (string, error) {
	if strings.TrimSpace(sourceHTML) == "" {
		return "", nil
	}

	// 1. Mask HTML tags & collapse adjacent tags/whitespace into Mustache tokens {{M#}}
	maskedText, tokenMap, stats := MaskHTML(sourceHTML)
	if len(tokenMap) == 0 {
		maskedText = sourceHTML
	}

	firstToken := ""
	lastToken := ""
	if stats.TokenCount > 0 {
		firstToken = "{{M0}}"
		lastToken = fmt.Sprintf("{{M%d}}", stats.TokenCount-1)
	}

	var translatedMasked string
	var translationErr error

	// 2. Try DeepL first if available
	if s.deepLSvc != nil && s.deepLSvc.IsConfigured() {
		s.taskMgr.UpdateTaskProgress(taskID, fmt.Sprintf("Translating %s via DeepL (masked chars saved: %d / %.1f%%)...", label, stats.CharactersSaved, stats.PercentSaved))
		res, err := s.deepLSvc.TranslateSingle(ctx, maskedText, sourceLang, targetLang)
		if err == nil {
			translatedMasked = res
		} else {
			translationErr = err
			s.taskMgr.UpdateTaskProgress(taskID, fmt.Sprintf("DeepL %s translation failed (%v), falling back to LLM...", label, err))
		}
	}

	// 3. Fallback to LLM if DeepL is unconfigured or failed
	if translatedMasked == "" {
		s.taskMgr.UpdateTaskProgress(taskID, fmt.Sprintf("Translating %s via LLM sister-document engine...", label))

		completionRequirement := ""
		if lastToken != "" {
			completionRequirement = fmt.Sprintf("You MUST translate the complete text from %s through the final closing token %s. Never stop early.", firstToken, lastToken)
		}

		htmlSystemPrompt := fmt.Sprintf(`You are an elite bilingual author and professional translator.
Translate the provided text from %s into %s so that the result reads as an authentic, compelling "sister document"—maintaining the original work's depth, eloquence, tone, and stylistic nuance without sounding like a robotic word-for-word translation.

CRITICAL RULES:
1. MUSTACHE TOKENS: The text contains layout tokens formatted as {{M0}}, {{M1}}, {{M2}}, etc.
   - You MUST preserve every single token {{M#}} EXACTLY in place.
   - Do NOT translate, alter, delete, or add any {{M#}} tokens.
2. COMPLETION: %s
3. ZERO PREAMBLE / ZERO CONVERSATIONAL FILLER:
   - Begin your output IMMEDIATELY with the translation or first token.
   - NEVER say "Here is the translation", "I understand", "Paragraph 1:", or similar introductory/commentary text.
   - Do NOT wrap output in markdown code blocks (NO `+"```"+` or `+"```html"+`).`,
			strings.ToUpper(sourceLang), targetFullName, completionRequirement)

		llmRes, err := s.llmSvc.CallLLM(ctx, htmlSystemPrompt, maskedText)
		if err != nil {
			if translationErr != nil {
				return "", fmt.Errorf("both DeepL (%v) and LLM (%w) failed", translationErr, err)
			}
			return "", fmt.Errorf("LLM translation failed: %w", err)
		}

		translatedMasked = CleanLLMTranslationOutput(llmRes, firstToken, lastToken)
	}

	// 4. Validate token completeness
	if len(tokenMap) > 0 {
		if missing, err := ValidateMaskTokens(translatedMasked, tokenMap); err != nil {
			// Log missing tokens for diagnostic visibility
			s.taskMgr.UpdateTaskProgress(taskID, fmt.Sprintf("Warning: %d tokens missing after translation (%v). Restoring available tokens.", len(missing), missing))
		}
	}

	// 5. Unmask back to full HTML
	unmaskedHTML := UnmaskHTML(translatedMasked, tokenMap)
	return unmaskedHTML, nil
}

func stripBackticks(input string) string {
	s := strings.TrimSpace(input)
	if strings.HasPrefix(s, "```") {
		lines := strings.Split(s, "\n")
		if len(lines) >= 2 && strings.HasPrefix(lines[0], "```") {
			lines = lines[1:]
		}
		if len(lines) >= 1 && strings.HasPrefix(lines[len(lines)-1], "```") {
			lines = lines[:len(lines)-1]
		}
		s = strings.Join(lines, "\n")
	}
	return strings.TrimSpace(s)
}
