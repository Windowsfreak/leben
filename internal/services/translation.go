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
		taskMgr:   taskMgr,
	}
}

type translatedMetadata struct {
	Title   string   `json:"title"`
	Summary string   `json:"summary"`
	Tags    []string `json:"tags"`
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

		s.taskMgr.UpdateTaskProgress(taskID, fmt.Sprintf("[%d/%d] Translating metadata to %s...", idx+1, len(targets), langFullName))

		metaPrompt := fmt.Sprintf(`You are an expert content translator. Translate the following tile metadata from %s into %s.
Respond ONLY with a valid JSON object (no markdown, no backticks):
{
  "title": "Translated Title",
  "summary": "Translated summary...",
  "tags": ["tag1", "tag2"]
}`, strings.ToUpper(sourceTile.Language), langFullName)

		metaUser := fmt.Sprintf("Source Title: %s\nSource Tags: %s\nSource Summary: %s",
			sourceTile.Title, strings.Join(sourceTile.Tags, ", "), sourceTile.Summary)

		metaRes, err := s.llmSvc.CallLLM(ctx, metaPrompt, metaUser)
		if err != nil {
			s.taskMgr.FailTask(taskID, fmt.Errorf("LLM metadata translation failed for %s: %w", tLang, err))
			return
		}

		cleanJSON := stripBackticks(metaRes)
		var transMeta translatedMetadata
		if err := json.Unmarshal([]byte(cleanJSON), &transMeta); err != nil {
			s.taskMgr.FailTask(taskID, fmt.Errorf("invalid metadata JSON from LLM (%s): %w", tLang, err))
			return
		}

		// Translate Teaser
		s.taskMgr.UpdateTaskProgress(taskID, fmt.Sprintf("[%d/%d] Translating HTML teaser to %s...", idx+1, len(targets), langFullName))
		htmlPrompt := fmt.Sprintf("You are an expert HTML translator. Translate all human-readable text in the provided HTML snippet into %s.\nKeep all HTML tags, structure, classes, IDs, icons (<i class=\"...\"></i>), and attributes intact.\nRespond ONLY with the translated HTML string. Do not include markdown code block formatting (no backticks like ```html).", langFullName)

		translatedTeaser := ""
		if sourceTile.HTMLTeaser != "" {
			teaserRes, err := s.llmSvc.CallLLM(ctx, htmlPrompt, sourceTile.HTMLTeaser)
			if err != nil {
				s.taskMgr.FailTask(taskID, fmt.Errorf("LLM teaser translation failed for %s: %w", tLang, err))
				return
			}
			translatedTeaser = stripBackticks(teaserRes)
		}

		// Translate content file if exists
		targetContentFile := ""
		if sourceTile.ContentFile != "" {
			s.taskMgr.UpdateTaskProgress(taskID, fmt.Sprintf("[%d/%d] Translating content file to %s...", idx+1, len(targets), langFullName))
			sourceFilePath := filepath.Join(contentsDir, sourceTile.ContentFile)
			if contentBytes, err := os.ReadFile(sourceFilePath); err == nil {
				contentRes, err := s.llmSvc.CallLLM(ctx, htmlPrompt, string(contentBytes))
				if err != nil {
					s.taskMgr.FailTask(taskID, fmt.Errorf("LLM content translation failed for %s: %w", tLang, err))
					return
				}
				translatedContent := stripBackticks(contentRes)

				baseName := strings.TrimSuffix(sourceTile.ContentFile, filepath.Ext(sourceTile.ContentFile))
				re := regexp.MustCompile(`[_\-][a-zA-Z]{2}$`)
				baseClean := re.ReplaceAllString(baseName, "")
				targetContentFile = fmt.Sprintf("%s_%s.html", baseClean, tLang)

				_ = os.WriteFile(filepath.Join(contentsDir, targetContentFile), []byte(translatedContent), 0644)
			}
		}

		tags := transMeta.Tags
		if len(tags) == 0 {
			tags = sourceTile.Tags
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
			Tags:        tags,
			Type:        sourceTile.Type,
			Link:        sourceTile.Link,
			ContentFile: targetContentFile,
			Visible:     sourceTile.Visible,
			Secret:      sourceTile.Secret,
			AccentColor: sourceTile.AccentColor,
			Background:  sourceTile.Background,
			SortOrder:   sourceTile.SortOrder,
		}

		// Check if target tile already exists to retain ID
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
