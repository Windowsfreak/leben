package services

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/windowsfreak/leben/internal/config"
	"github.com/windowsfreak/leben/internal/db"
	"github.com/windowsfreak/leben/internal/models"
)

var ErrTileNotFound = errors.New("tile not found")

type TileService struct {
	cfg      *config.Config
	database *db.DB
	ollama   *OllamaService
}

func NewTileService(cfg *config.Config, database *db.DB, ollama *OllamaService) *TileService {
	return &TileService{
		cfg:      cfg,
		database: database,
		ollama:   ollama,
	}
}

func FormatTileDocumentText(name, language string, tags string, summary string) string {
	return fmt.Sprintf("%s %s, %s: %s", name, strings.ToUpper(language), tags, summary)
}

func (s *TileService) SearchTiles(ctx context.Context, prefLang, queryStr string, refCodes []string, showInvisible bool, offset, limit int) ([]*models.Tile, error) {
	dbLimit := limit
	if dbLimit <= 0 {
		dbLimit = 999999
	}
	refCodesStr := strings.Join(refCodes, ",")

	queryStr = strings.TrimSpace(queryStr)
	if queryStr != "" {
		searchText := queryStr + " " + strings.ToUpper(prefLang)
		queryVec, err := s.ollama.GetEmbedding(ctx, searchText, "query")
		if err != nil {
			// Fallback zero vector if embedding fails
			queryVec = make([]float64, 768)
		}
		vecStr := db.VectorToString(queryVec)

		var sqlQuery string
		var rows *sql.Rows
		if prefLang == "all" {
			sqlQuery = `
				SELECT id, name, language, tags, title, html_teaser, 
					   summary, link, type, content_file, 
					   visible, secret, accent_color, background, embedding, sort_order, created_at, updated_at,
					   (embedding <=> $1::vector) as distance
				FROM tiles
				WHERE ($2 = true OR (visible = true AND (secret = '' OR secret = ANY(string_to_array($3, ',')))))
				ORDER BY distance ASC, sort_order ASC, id ASC 
				LIMIT $4 OFFSET $5
			`
			rows, err = s.database.QueryContext(ctx, sqlQuery, vecStr, showInvisible, refCodesStr, dbLimit, offset)
		} else {
			sqlQuery = `
				WITH resolved_tiles AS (
					SELECT *,
						   (embedding <=> $1::vector) as distance,
						   ROW_NUMBER() OVER (
							   PARTITION BY name 
							   ORDER BY 
								   CASE WHEN language = $2 THEN 1 
										ELSE 2 
								   END,
								   (embedding <=> $1::vector) ASC
						   ) as rn
					FROM tiles
					WHERE ($3 = true OR (visible = true AND (secret = '' OR secret = ANY(string_to_array($4, ',')))))
				)
				SELECT id, name, language, tags, title, html_teaser, 
					   summary, link, type, content_file, 
					   visible, secret, accent_color, background, embedding, sort_order, created_at, updated_at, distance
				FROM resolved_tiles 
				WHERE rn = 1 
				ORDER BY distance ASC, sort_order ASC, id ASC 
				LIMIT $5 OFFSET $6
			`
			rows, err = s.database.QueryContext(ctx, sqlQuery, vecStr, prefLang, showInvisible, refCodesStr, dbLimit, offset)
		}
		if err != nil {
			return nil, fmt.Errorf("search query error: %w", err)
		}
		defer rows.Close()

		var results []*models.Tile
		for rows.Next() {
			var tile models.Tile
			var tagsStr, vecStr sql.NullString
			var link, contentFile, background sql.NullString

			err := rows.Scan(
				&tile.ID,
				&tile.Name,
				&tile.Language,
				&tagsStr,
				&tile.Title,
				&tile.HTMLTeaser,
				&tile.Summary,
				&link,
				&tile.Type,
				&contentFile,
				&tile.Visible,
				&tile.Secret,
				&tile.AccentColor,
				&background,
				&vecStr,
				&tile.SortOrder,
				&tile.CreatedAt,
				&tile.UpdatedAt,
				&tile.Distance,
			)
			if err != nil {
				return nil, err
			}
			if tile.Distance > 0 && tile.Distance <= 2.0 {
				sim := math.Round((1.0-tile.Distance)*100) / 100
				if sim < 0 {
					sim = 0
				}
				if sim > 1 {
					sim = 1
				}
				tile.Score = &sim
			}
			if tagsStr.Valid {
				tile.Tags = db.PostgresToTags(tagsStr.String)
			}
			if link.Valid {
				tile.Link = link.String
			}
			if contentFile.Valid {
				tile.ContentFile = contentFile.String
			}
			if background.Valid {
				tile.Background = background.String
			}
			tile.Protected = (tile.Secret != "")
			if !showInvisible {
				tile.Secret = ""
			}
			results = append(results, &tile)
		}
		return results, nil
	}

	// No query: sort by sort_order
	var sqlQuery string
	var rows *sql.Rows
	var err error
	if prefLang == "all" {
		sqlQuery = `
			SELECT id, name, language, tags, title, html_teaser, 
				   summary, link, type, content_file, 
				   visible, secret, accent_color, background, embedding, sort_order, created_at, updated_at
			FROM tiles
			WHERE ($1 = true OR (visible = true AND (secret = '' OR secret = ANY(string_to_array($2, ',')))))
			ORDER BY sort_order ASC, name ASC, language ASC
			LIMIT $3 OFFSET $4
		`
		rows, err = s.database.QueryContext(ctx, sqlQuery, showInvisible, refCodesStr, dbLimit, offset)
	} else {
		sqlQuery = `
			WITH resolved_tiles AS (
				SELECT *,
					   ROW_NUMBER() OVER (
						   PARTITION BY name 
						   ORDER BY 
							   CASE WHEN language = $1 THEN 1 
									ELSE 2 
							   END,
							   sort_order ASC, created_at DESC, id ASC
					   ) as rn
				FROM tiles
				WHERE ($2 = true OR (visible = true AND (secret = '' OR secret = ANY(string_to_array($3, ',')))))
			)
			SELECT id, name, language, tags, title, html_teaser, 
				   summary, link, type, content_file, 
				   visible, secret, accent_color, background, embedding, sort_order, created_at, updated_at
			FROM resolved_tiles 
			WHERE rn = 1 
			ORDER BY sort_order ASC, created_at DESC, id ASC
			LIMIT $4 OFFSET $5
		`
		rows, err = s.database.QueryContext(ctx, sqlQuery, prefLang, showInvisible, refCodesStr, dbLimit, offset)
	}
	if err != nil {
		return nil, fmt.Errorf("tiles list query error: %w", err)
	}
	defer rows.Close()

	var results []*models.Tile
	for rows.Next() {
		tile, err := db.ScanTile(rows)
		if err != nil {
			return nil, err
		}
		if !showInvisible {
			tile.Secret = ""
		}
		results = append(results, tile)
	}
	return results, nil
}

func (s *TileService) GetSimilarTiles(ctx context.Context, name, prefLang string, refCodes []string, showInvisible bool, limit, offset int) ([]*models.Tile, error) {
	dbLimit := limit
	if dbLimit <= 0 {
		dbLimit = 999999
	}
	refCodesStr := strings.Join(refCodes, ",")

	sqlQuery := `
		WITH source_tile AS (
			SELECT embedding 
			FROM tiles 
			WHERE name = $1 
			ORDER BY CASE WHEN language = $2 THEN 1 ELSE 2 END, created_at ASC
			LIMIT 1
		),
		resolved_tiles AS (
			SELECT *,
				   (embedding <=> (SELECT embedding FROM source_tile)) as distance,
				   ROW_NUMBER() OVER (
					   PARTITION BY name 
					   ORDER BY 
						   CASE WHEN language = $2 THEN 1 
								ELSE 2 
						   END,
						   (embedding <=> (SELECT embedding FROM source_tile)) ASC
					   ) as rn
			FROM tiles
			WHERE ($3 = true OR (visible = true AND (secret = '' OR secret = ANY(string_to_array($4, ','))))) AND name != $1
		)
		SELECT id, name, language, tags, title, html_teaser, 
			   summary, link, type, content_file, 
			   visible, secret, accent_color, background, embedding, sort_order, created_at, updated_at, distance
		FROM resolved_tiles 
		WHERE rn = 1 AND (SELECT embedding FROM source_tile) IS NOT NULL
		ORDER BY distance ASC, sort_order ASC, id ASC 
		LIMIT $5 OFFSET $6
	`
	rows, err := s.database.QueryContext(ctx, sqlQuery, name, prefLang, showInvisible, refCodesStr, dbLimit, offset)
	if err != nil {
		return nil, fmt.Errorf("similar tiles query error: %w", err)
	}
	defer rows.Close()

	var results []*models.Tile
	for rows.Next() {
		var tile models.Tile
		var tagsStr, vecStr sql.NullString
		var link, contentFile, background sql.NullString

		err := rows.Scan(
			&tile.ID,
			&tile.Name,
			&tile.Language,
			&tagsStr,
			&tile.Title,
			&tile.HTMLTeaser,
			&tile.Summary,
			&link,
			&tile.Type,
			&contentFile,
			&tile.Visible,
			&tile.Secret,
			&tile.AccentColor,
			&background,
			&vecStr,
			&tile.SortOrder,
			&tile.CreatedAt,
			&tile.UpdatedAt,
			&tile.Distance,
		)
		if err != nil {
			return nil, err
		}
		if tile.Distance > 0 && tile.Distance <= 2.0 {
			sim := math.Round((1.0-tile.Distance)*100) / 100
			if sim < 0 {
				sim = 0
			}
			if sim > 1 {
				sim = 1
			}
			tile.Score = &sim
		}
		if tagsStr.Valid {
			tile.Tags = db.PostgresToTags(tagsStr.String)
		}
		if link.Valid {
			tile.Link = link.String
		}
		if contentFile.Valid {
			tile.ContentFile = contentFile.String
		}
		if background.Valid {
			tile.Background = background.String
		}
		tile.Protected = (tile.Secret != "")
		if !showInvisible {
			tile.Secret = ""
		}
		results = append(results, &tile)
	}
	return results, nil
}

func (s *TileService) GetTileExact(ctx context.Context, name, lang string, refCodes []string, showInvisible bool) (*models.Tile, error) {
	refCodesStr := strings.Join(refCodes, ",")
	name = strings.ToLower(strings.TrimSpace(name))

	sqlQuery := `
		SELECT id, name, language, tags, title, html_teaser, summary, link, type, content_file, visible, secret, accent_color, background, embedding, sort_order, created_at, updated_at
		FROM tiles 
		WHERE name = $1 AND language = $2 
		  AND ($3 = true OR (visible = true AND (secret = '' OR secret = ANY(string_to_array($4, ',')))))
		LIMIT 1
	`
	row := s.database.QueryRowContext(ctx, sqlQuery, name, lang, showInvisible, refCodesStr)
	tile, err := db.ScanTile(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("%w: '%s' (lang: %s)", ErrTileNotFound, name, lang)
		}
		return nil, err
	}

	if !showInvisible {
		tile.Secret = ""
	}
	return tile, nil
}

func (s *TileService) GetTile(ctx context.Context, name, prefLang string, refCodes []string, showInvisible bool) (*models.Tile, error) {
	refCodesStr := strings.Join(refCodes, ",")
	name = strings.ToLower(strings.TrimSpace(name))
	prefLang = strings.ToLower(strings.TrimSpace(prefLang))
	if prefLang == "" {
		prefLang = "de"
	}

	sqlQuery := `
		SELECT id, name, language, tags, title, html_teaser, summary, link, type, content_file, visible, secret, accent_color, background, embedding, sort_order, created_at, updated_at
		FROM tiles 
		WHERE name = $1 
		  AND ($2 = true OR (visible = true AND (secret = '' OR secret = ANY(string_to_array($3, ',')))))
		ORDER BY
			CASE
				WHEN language = $4 THEN 1
				WHEN language = 'de' THEN 2
				WHEN language = 'en' THEN 3
				ELSE 4
			END,
			sort_order ASC, created_at DESC
		LIMIT 1
	`
	row := s.database.QueryRowContext(ctx, sqlQuery, name, showInvisible, refCodesStr, prefLang)
	tile, err := db.ScanTile(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("%w: '%s'", ErrTileNotFound, name)
		}
		return nil, err
	}

	if !showInvisible {
		tile.Secret = ""
	}
	return tile, nil
}

func (s *TileService) GetTileInfo(ctx context.Context, name string, refCodes []string, showInvisible bool) ([]*models.TileSummary, error) {
	refCodesStr := strings.Join(refCodes, ",")
	sqlQuery := `
		SELECT id, name, language, title, created_at, updated_at, visible, content_file
		FROM tiles
		WHERE name = $1
		  AND ($2 = true OR (visible = true AND (secret = '' OR secret = ANY(string_to_array($3, ',')))))
		ORDER BY created_at ASC
	`
	rows, err := s.database.QueryContext(ctx, sqlQuery, name, showInvisible, refCodesStr)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var summaries []*models.TileSummary
	for rows.Next() {
		var s models.TileSummary
		var cf sql.NullString
		if err := rows.Scan(&s.ID, &s.Name, &s.Lang, &s.Title, &s.CreatedAt, &s.UpdatedAt, &s.Visible, &cf); err != nil {
			return nil, err
		}
		if cf.Valid {
			s.ContentFile = cf.String
		}
		summaries = append(summaries, &s)
	}
	return summaries, nil
}

func (s *TileService) GetAllTiles(ctx context.Context) ([]*models.Tile, error) {
	sqlQuery := `
		SELECT id, name, language, tags, title, html_teaser, summary, link, type, content_file, visible, secret, accent_color, background, embedding, sort_order, created_at, updated_at
		FROM tiles 
		ORDER BY name ASC, language ASC
	`
	rows, err := s.database.QueryContext(ctx, sqlQuery)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tiles []*models.Tile
	for rows.Next() {
		tile, err := db.ScanTile(rows)
		if err != nil {
			return nil, err
		}
		tiles = append(tiles, tile)
	}
	return tiles, nil
}

func (s *TileService) SaveTile(ctx context.Context, tile *models.Tile) error {
	tile.Name = strings.ToLower(strings.TrimSpace(tile.Name))
	tile.Language = strings.ToLower(strings.TrimSpace(tile.Language))
	if tile.Name == "" || tile.Language == "" {
		return fmt.Errorf("name and language are required")
	}

	// Generate embedding if necessary (reuse existing vector if document text is unchanged)
	docText := FormatTileDocumentText(tile.Name, tile.Language, tile.Tags, tile.Summary)
	var vecStr string
	if orig, err := s.GetTileExact(ctx, tile.Name, tile.Language, nil, true); err == nil && orig != nil {
		oldDocText := FormatTileDocumentText(orig.Name, orig.Language, orig.Tags, orig.Summary)
		if oldDocText == docText && len(orig.Embedding) > 0 {
			vecStr = db.VectorToString(orig.Embedding)
		}
	}
	if vecStr == "" {
		vec, err := s.ollama.GetEmbedding(ctx, docText, "document")
		if err != nil {
			// Zero vector fallback
			vec = make([]float64, 768)
		}
		vecStr = db.VectorToString(vec)
	}
	pgTags := db.TagsToPostgres(tile.Tags)

	if tile.SortOrder == 0 {
		tile.SortOrder = 100
	}
	if tile.AccentColor == "" {
		tile.AccentColor = "#fbbf24"
	}
	// Sanitize and normalize Type: strip any parenthesized annotation (e.g. doc(15000b) -> doc)
	tile.Type = strings.TrimSpace(tile.Type)
	if idx := strings.Index(tile.Type, "("); idx != -1 {
		tile.Type = strings.TrimSpace(tile.Type[:idx])
	}
	if tile.Type != "link" {
		tile.Type = "doc"
	}

	sqlQuery := `
		INSERT INTO tiles (
			name, language, tags, title, html_teaser, summary, link, type, content_file,
			visible, secret, accent_color, background, embedding, sort_order, created_at, updated_at
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14::vector, $15, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP
		)
		ON CONFLICT (name, language) DO UPDATE SET
			tags = EXCLUDED.tags,
			title = EXCLUDED.title,
			html_teaser = EXCLUDED.html_teaser,
			summary = EXCLUDED.summary,
			link = EXCLUDED.link,
			type = EXCLUDED.type,
			content_file = EXCLUDED.content_file,
			visible = EXCLUDED.visible,
			secret = EXCLUDED.secret,
			accent_color = EXCLUDED.accent_color,
			background = EXCLUDED.background,
			embedding = EXCLUDED.embedding,
			sort_order = EXCLUDED.sort_order,
			updated_at = CURRENT_TIMESTAMP
		RETURNING id
	`
	return s.database.QueryRowContext(ctx, sqlQuery,
		tile.Name, tile.Language, pgTags, tile.Title, tile.HTMLTeaser, tile.Summary,
		tile.Link, tile.Type, tile.ContentFile, tile.Visible, tile.Secret, tile.AccentColor,
		tile.Background, vecStr, tile.SortOrder,
	).Scan(&tile.ID)
}

func (s *TileService) PatchTiles(ctx context.Context, patches []models.TilePatchDTO) (*models.BatchPatchResult, error) {
	result := &models.BatchPatchResult{
		Total:   len(patches),
		Results: make([]models.TilePatchItemResult, 0, len(patches)),
	}

	tx, err := s.database.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	for _, patch := range patches {
		name, lang, err := patch.Normalize()
		if err != nil {
			result.Failed++
			errMsg := fmt.Sprintf("invalid patch spec: %v", err)
			result.Errors = append(result.Errors, errMsg)
			result.Results = append(result.Results, models.TilePatchItemResult{
				Name:   patch.Name,
				Lang:   patch.Lang,
				Status: "error",
				Error:  errMsg,
			})
			continue
		}

		selectSQL := `
			SELECT id, name, language, tags, title, html_teaser, summary, link, type, content_file,
			       visible, secret, accent_color, background, embedding, sort_order, created_at, updated_at
			FROM tiles
			WHERE name = $1 AND language = $2
			FOR UPDATE
		`
		row := tx.QueryRowContext(ctx, selectSQL, name, lang)
		existing, err := db.ScanTile(row)
		if err != nil {
			result.Failed++
			errMsg := fmt.Sprintf("tile '%s:%s' not found", name, lang)
			result.Errors = append(result.Errors, errMsg)
			result.Results = append(result.Results, models.TilePatchItemResult{
				Name:   name,
				Lang:   lang,
				Status: "error",
				Error:  errMsg,
			})
			continue
		}

		targetName := name
		if patch.NewName != nil {
			trimmedNew := strings.ToLower(strings.TrimSpace(*patch.NewName))
			if trimmedNew != "" && trimmedNew != name {
				var exists bool
				checkCollisionSQL := `SELECT EXISTS(SELECT 1 FROM tiles WHERE name = $1 AND language = $2 AND id != $3)`
				if err := tx.QueryRowContext(ctx, checkCollisionSQL, trimmedNew, lang, existing.ID).Scan(&exists); err != nil {
					result.Failed++
					errMsg := fmt.Sprintf("error checking collision for '%s:%s': %v", trimmedNew, lang, err)
					result.Errors = append(result.Errors, errMsg)
					result.Results = append(result.Results, models.TilePatchItemResult{
						Name:   name,
						Lang:   lang,
						Status: "error",
						Error:  errMsg,
					})
					continue
				}
				if exists {
					result.Failed++
					errMsg := fmt.Sprintf("cannot rename '%s:%s': slug '%s' already exists for language '%s'", name, lang, trimmedNew, lang)
					result.Errors = append(result.Errors, errMsg)
					result.Results = append(result.Results, models.TilePatchItemResult{
						Name:    name,
						Lang:    lang,
						NewName: trimmedNew,
						Status:  "error",
						Error:   errMsg,
					})
					continue
				}
				targetName = trimmedNew
			}
		}

		oldDocText := FormatTileDocumentText(existing.Name, existing.Language, existing.Tags, existing.Summary)

		existing.Name = targetName
		if patch.Title != nil {
			existing.Title = *patch.Title
		}
		if patch.Summary != nil {
			existing.Summary = *patch.Summary
		}
		if patch.HTMLTeaser != nil {
			existing.HTMLTeaser = *patch.HTMLTeaser
		}
		if patch.ContentFile != nil {
			existing.ContentFile = *patch.ContentFile
		}
		if patch.Tags != nil {
			existing.Tags = *patch.Tags
		}
		if patch.Type != nil {
			t := strings.TrimSpace(*patch.Type)
			if idx := strings.Index(t, "("); idx != -1 {
				t = strings.TrimSpace(t[:idx])
			}
			if t != "link" {
				t = "doc"
			}
			existing.Type = t
		}
		if patch.Link != nil {
			existing.Link = *patch.Link
		}
		if patch.Secret != nil {
			existing.Secret = *patch.Secret
		}
		if patch.AccentColor != nil {
			existing.AccentColor = *patch.AccentColor
		}
		if patch.Background != nil {
			existing.Background = *patch.Background
		}
		if patch.Visible != nil {
			existing.Visible = *patch.Visible
		}
		if patch.SortOrder != nil {
			existing.SortOrder = *patch.SortOrder
		}

		newDocText := FormatTileDocumentText(existing.Name, existing.Language, existing.Tags, existing.Summary)

		var vecStr string
		if oldDocText == newDocText && len(existing.Embedding) > 0 {
			vecStr = db.VectorToString(existing.Embedding)
		} else {
			vec, err := s.ollama.GetEmbedding(ctx, newDocText, "document")
			if err != nil {
				vec = make([]float64, 768)
			}
			vecStr = db.VectorToString(vec)
		}

		pgTags := db.TagsToPostgres(existing.Tags)

		updateSQL := `
			UPDATE tiles SET
				name = $1, tags = $2, title = $3, html_teaser = $4, summary = $5,
				link = $6, type = $7, content_file = $8, visible = $9, secret = $10,
				accent_color = $11, background = $12, embedding = $13::vector,
				sort_order = $14, updated_at = CURRENT_TIMESTAMP
			WHERE id = $15
		`
		_, err = tx.ExecContext(ctx, updateSQL,
			existing.Name, pgTags, existing.Title, existing.HTMLTeaser, existing.Summary,
			existing.Link, existing.Type, existing.ContentFile, existing.Visible, existing.Secret,
			existing.AccentColor, existing.Background, vecStr, existing.SortOrder, existing.ID,
		)
		if err != nil {
			result.Failed++
			errMsg := fmt.Sprintf("failed to update tile '%s:%s': %v", name, lang, err)
			result.Errors = append(result.Errors, errMsg)
			result.Results = append(result.Results, models.TilePatchItemResult{
				Name:   name,
				Lang:   lang,
				Status: "error",
				Error:  errMsg,
			})
			continue
		}

		result.Succeeded++
		itemRes := models.TilePatchItemResult{
			Name:   name,
			Lang:   lang,
			Status: "ok",
		}
		if targetName != name {
			itemRes.NewName = targetName
		}
		result.Results = append(result.Results, itemRes)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("failed to commit transaction: %w", err)
	}

	if result.Failed == 0 {
		result.Status = "success"
	} else if result.Succeeded > 0 {
		result.Status = "partial_success"
	} else {
		result.Status = "error"
	}

	return result, nil
}

func (s *TileService) GetTilesBySpec(ctx context.Context, specs []models.TileSpec, defaultLang string, exact bool, refCodes []string, showInvisible bool) ([]*models.Tile, error) {
	if len(specs) == 0 {
		return []*models.Tile{}, nil
	}

	results := make([]*models.Tile, 0, len(specs))
	for _, spec := range specs {
		name := strings.ToLower(strings.TrimSpace(spec.Name))
		lang := strings.ToLower(strings.TrimSpace(spec.Lang))
		hasExplicitLang := lang != ""
		if lang == "" {
			lang = defaultLang
		}
		if lang == "" {
			lang = "de"
		}

		var tile *models.Tile
		var err error
		if exact && hasExplicitLang {
			tile, err = s.GetTileExact(ctx, name, lang, refCodes, showInvisible)
		} else {
			tile, err = s.GetTile(ctx, name, lang, refCodes, showInvisible)
		}
		if err == nil && tile != nil {
			results = append(results, tile)
		}
	}

	return results, nil
}

func (s *TileService) DeleteTile(ctx context.Context, id int) error {
	_, err := s.database.ExecContext(ctx, "DELETE FROM tiles WHERE id = $1", id)
	return err
}

type FrontendConfig struct {
	SupportedLanguages map[string]string `json:"supported_languages"`
}

func LoadSupportedLanguages(webDir string) map[string]string {
	defaultMap := map[string]string{
		"de": "Deutsch",
		"en": "English",
	}
	configPath := filepath.Join(webDir, "config.json")
	data, err := os.ReadFile(configPath)
	if err != nil {
		return defaultMap
	}

	var fc FrontendConfig
	if err := json.Unmarshal(data, &fc); err != nil || len(fc.SupportedLanguages) == 0 {
		return defaultMap
	}

	return fc.SupportedLanguages
}

func (s *TileService) GetTranslationStatus(ctx context.Context, tileName string) (map[string]*models.TileTranslationMatrix, error) {
	allTiles, err := s.GetAllTiles(ctx)
	if err != nil {
		return nil, err
	}

	grouped := make(map[string][]*models.Tile)
	for _, t := range allTiles {
		if tileName == "" || t.Name == tileName {
			grouped[t.Name] = append(grouped[t.Name], t)
		}
	}

	supportedMap := LoadSupportedLanguages(s.cfg.Server.WebDir)
	var supportedLangs []string
	for code := range supportedMap {
		supportedLangs = append(supportedLangs, code)
	}
	contentsDir := filepath.Join(s.cfg.Server.WebDir, "content")

	matrix := make(map[string]*models.TileTranslationMatrix)

	for name, siblings := range grouped {
		if len(siblings) == 0 {
			continue
		}
		var sourceRow *models.Tile
		for _, sib := range siblings {
			if sib.Language == "de" {
				sourceRow = sib
				break
			}
		}
		if sourceRow == nil {
			sourceRow = siblings[0]
		}
		sourceLang := sourceRow.Language
		sourceDBTime := sourceRow.UpdatedAt

		var sourceFileTime time.Time
		if sourceRow.ContentFile != "" {
			fPath := filepath.Join(contentsDir, sourceRow.ContentFile)
			if info, err := os.Stat(fPath); err == nil {
				sourceFileTime = info.ModTime()
			}
		}

		effSourceMTime := sourceDBTime
		if sourceFileTime.After(effSourceMTime) {
			effSourceMTime = sourceFileTime
		}

		langMatrix := make(map[string]models.TranslationStatusItem)
		hasStale := false

		for _, sib := range siblings {
			isSource := sib.Language == sourceLang
			var sibFileTime *time.Time
			if sib.ContentFile != "" {
				fPath := filepath.Join(contentsDir, sib.ContentFile)
				if info, err := os.Stat(fPath); err == nil {
					mt := info.ModTime()
					sibFileTime = &mt
				}
			}

			status := "up_to_date"
			if isSource {
				status = "source"
			} else if sib.UpdatedAt.Before(effSourceMTime) {
				status = "stale"
				hasStale = true
			}

			langMatrix[sib.Language] = models.TranslationStatusItem{
				ID:          sib.ID,
				Language:    sib.Language,
				IsSource:    isSource,
				Status:      status,
				CreatedAt:   sib.CreatedAt,
				UpdatedAt:   sib.UpdatedAt,
				ContentFile: sib.ContentFile,
				FileMTime:   sibFileTime,
			}
		}

		var presentLangs []string
		for l := range langMatrix {
			presentLangs = append(presentLangs, l)
		}

		var missing []string
		for _, supp := range supportedLangs {
			found := false
			for _, p := range presentLangs {
				if p == supp {
					found = true
					break
				}
			}
			if !found {
				missing = append(missing, supp)
			}
		}

		matrix[name] = &models.TileTranslationMatrix{
			Name:                 name,
			SourceLanguage:       sourceLang,
			EffectiveSourceMTime: effSourceMTime,
			HasStaleTranslation:  hasStale,
			Languages:            langMatrix,
			MissingLanguages:     missing,
		}
	}

	return matrix, nil
}

func (s *TileService) ToggleVisibility(ctx context.Context, id int) error {
	_, err := s.database.ExecContext(ctx, "UPDATE tiles SET visible = NOT visible, updated_at = CURRENT_TIMESTAMP WHERE id = $1", id)
	return err
}

func (s *TileService) CloneTile(ctx context.Context, id int) (*models.Tile, error) {
	var orig models.Tile
	sqlQuery := `
		SELECT id, name, language, tags, title, html_teaser, summary, link, type, content_file, visible, secret, accent_color, background, sort_order
		FROM tiles WHERE id = $1
	`
	var tagsStr sql.NullString
	var link, contentFile, background sql.NullString
	err := s.database.QueryRowContext(ctx, sqlQuery, id).Scan(
		&orig.ID, &orig.Name, &orig.Language, &tagsStr, &orig.Title, &orig.HTMLTeaser, &orig.Summary,
		&link, &orig.Type, &contentFile, &orig.Visible, &orig.Secret, &orig.AccentColor, &orig.Background, &orig.SortOrder,
	)
	if err != nil {
		return nil, err
	}
	if tagsStr.Valid {
		orig.Tags = db.PostgresToTags(tagsStr.String)
	}
	if link.Valid {
		orig.Link = link.String
	}
	if contentFile.Valid {
		orig.ContentFile = contentFile.String
	}
	if background.Valid {
		orig.Background = background.String
	}

	clone := orig
	clone.ID = 0
	clone.Name = orig.Name + "-copy"
	clone.Title = orig.Title + " (Copy)"
	if err := s.SaveTile(ctx, &clone); err != nil {
		return nil, err
	}
	return &clone, nil
}

func (s *TileService) RefreshVectors(ctx context.Context) (int, error) {
	allTiles, err := s.GetAllTiles(ctx)
	if err != nil {
		return 0, err
	}
	count := 0
	for _, t := range allTiles {
		if err := s.SaveTile(ctx, t); err == nil {
			count++
		}
	}
	return count, nil
}

func (s *TileService) DeleteTileByName(ctx context.Context, name, lang string) error {
	name = strings.ToLower(strings.TrimSpace(name))
	lang = strings.ToLower(strings.TrimSpace(lang))
	if name == "" || lang == "" {
		return fmt.Errorf("name and language are required")
	}
	res, err := s.database.ExecContext(ctx, "DELETE FROM tiles WHERE name = $1 AND language = $2", name, lang)
	if err != nil {
		return err
	}
	rows, _ := res.RowsAffected()
	if rows == 0 {
		return ErrTileNotFound
	}
	return nil
}

func (s *TileService) ToggleVisibilityByName(ctx context.Context, name, lang string) error {
	name = strings.ToLower(strings.TrimSpace(name))
	lang = strings.ToLower(strings.TrimSpace(lang))
	if name == "" || lang == "" {
		return fmt.Errorf("name and language are required")
	}
	res, err := s.database.ExecContext(ctx, "UPDATE tiles SET visible = NOT visible, updated_at = CURRENT_TIMESTAMP WHERE name = $1 AND language = $2", name, lang)
	if err != nil {
		return err
	}
	rows, _ := res.RowsAffected()
	if rows == 0 {
		return ErrTileNotFound
	}
	return nil
}

func (s *TileService) CloneTileByName(ctx context.Context, name, lang string) (*models.Tile, error) {
	name = strings.ToLower(strings.TrimSpace(name))
	lang = strings.ToLower(strings.TrimSpace(lang))
	if name == "" || lang == "" {
		return nil, fmt.Errorf("name and language are required")
	}
	orig, err := s.GetTile(ctx, name, lang, nil, true)
	if err != nil {
		return nil, err
	}
	clone := *orig
	clone.ID = 0
	clone.Name = orig.Name + "-copy"
	clone.Title = orig.Title + " (Copy)"
	if err := s.SaveTile(ctx, &clone); err != nil {
		return nil, err
	}
	return &clone, nil
}

type ContentFileInfo struct {
	Name    string `json:"name"`
	Size    int64  `json:"size"`
	ModTime int64  `json:"mtime"`
}

func ListContentFiles(webDir string) ([]ContentFileInfo, error) {
	contentDir := filepath.Join(webDir, "content")
	entries, err := os.ReadDir(contentDir)
	if err != nil {
		return nil, err
	}
	var files []ContentFileInfo
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(strings.ToLower(e.Name()), ".html") {
			info, err := e.Info()
			if err != nil {
				continue
			}
			files = append(files, ContentFileInfo{
				Name:    e.Name(),
				Size:    info.Size(),
				ModTime: info.ModTime().Unix(),
			})
		}
	}
	return files, nil
}

func DeleteContentFile(webDir, filename string) error {
	filename = filepath.Base(filename)
	if filename == "" || filename == "." {
		return fmt.Errorf("filename required")
	}
	fPath := filepath.Join(webDir, "content", filename)
	if _, err := os.Stat(fPath); os.IsNotExist(err) {
		return fmt.Errorf("content file '%s' not found", filename)
	}
	return os.Remove(fPath)
}

func RenameContentFile(webDir, oldName, newName string) error {
	oldName = filepath.Base(oldName)
	newName = filepath.Base(newName)
	if oldName == "" || newName == "" || oldName == "." || newName == "." {
		return fmt.Errorf("old_name and new_name required")
	}
	contentDir := filepath.Join(webDir, "content")
	oldPath := filepath.Join(contentDir, oldName)
	newPath := filepath.Join(contentDir, newName)
	if _, err := os.Stat(oldPath); os.IsNotExist(err) {
		return fmt.Errorf("content file '%s' not found", oldName)
	}
	if _, err := os.Stat(newPath); err == nil {
		return fmt.Errorf("target content file '%s' already exists", newName)
	}
	return os.Rename(oldPath, newPath)
}

// ToRichTileDTO transforms a models.Tile into a models.TileDTO according to detail mode,
// crop limits, admin visibility, and sparse field projection.
func ToRichTileDTO(t *models.Tile, idx int, detail string, crop int, contentsDir string, qStr, similarName string, isAdmin bool, fieldSet map[string]bool) models.TileDTO {
	tagsStr := t.Tags
	dateStr := t.UpdatedAt.Format("2006-01-02")

	typeStr := t.Type
	if typeStr == "" {
		typeStr = "doc"
	}
	if t.ContentFile != "" {
		fPath := filepath.Join(contentsDir, t.ContentFile)
		if info, err := os.Stat(fPath); err == nil {
			typeStr = fmt.Sprintf("doc(%db)", info.Size())
		}
	}

	var score *float64
	if t.Score != nil {
		score = t.Score
	} else if (qStr != "" || similarName != "") && t.Distance > 0 && t.Distance <= 2.0 {
		sim := math.Round((1.0-t.Distance)*100) / 100
		if sim < 0 {
			sim = 0
		}
		if sim > 1 {
			sim = 1
		}
		score = &sim
	}

	summaryText := ""
	bodyText := ""
	teaserText := ""
	accentColor := ""
	background := ""
	contentFile := ""

	var visiblePtr *bool
	secret := ""
	sortOrder := 0

	switch detail {
	case "min":
		// Only core metadata: summary, body, teaser, accent, background are empty
	case "summary":
		summaryText = t.Summary
		contentFile = t.ContentFile
	case "full":
		summaryText = t.Summary
		teaserText = t.HTMLTeaser
		accentColor = t.AccentColor
		background = t.Background
		contentFile = t.ContentFile
		if t.ContentFile != "" {
			fPath := filepath.Join(contentsDir, t.ContentFile)
			if bytes, err := os.ReadFile(fPath); err == nil {
				bodyText = string(bytes)
			}
		} else if t.Link != "" {
			bodyText = "Link URL: " + t.Link
		} else {
			bodyText = t.HTMLTeaser
		}
		if isAdmin {
			vis := t.Visible
			visiblePtr = &vis
			secret = t.Secret
			sortOrder = t.SortOrder
		}
	default: // "snippet"
		detail = "snippet"
		summaryText = t.Summary
		contentFile = t.ContentFile
		if crop <= 0 {
			crop = 120
		}
	}

	if crop > 0 {
		if len(summaryText) > crop {
			summaryText = summaryText[:crop] + "..."
		}
		if len(bodyText) > crop {
			bodyText = bodyText[:crop] + "..."
		}
	}

	dto := models.TileDTO{
		Index:       idx,
		Name:        t.Name,
		Lang:        t.Language,
		Title:       t.Title,
		HTMLTeaser:  teaserText,
		Summary:     summaryText,
		Content:     bodyText,
		ContentFile: contentFile,
		Type:        typeStr,
		Tags:        tagsStr,
		Link:        t.Link,
		Date:        dateStr,
		Score:       score,
		AccentColor: accentColor,
		Background:  background,
		Visible:     visiblePtr,
		Secret:      secret,
		SortOrder:   sortOrder,
	}

	if len(fieldSet) > 0 {
		ApplyFieldMask(&dto, t, fieldSet)
	}

	return dto
}

// ApplyFieldMask filters TileDTO fields according to requested field names.
func ApplyFieldMask(dto *models.TileDTO, t *models.Tile, fieldSet map[string]bool) {
	if !fieldSet["index"] {
		dto.Index = 0
	}
	if !fieldSet["name"] {
		dto.Name = ""
	}
	if !fieldSet["lang"] && !fieldSet["language"] {
		dto.Lang = ""
	}
	if !fieldSet["title"] {
		dto.Title = ""
	}
	if !fieldSet["html_teaser"] && !fieldSet["teaser"] {
		dto.HTMLTeaser = ""
	}
	if !fieldSet["summary"] {
		dto.Summary = ""
	}
	if !fieldSet["content"] && !fieldSet["body"] {
		dto.Content = ""
	}
	if !fieldSet["content_file"] && !fieldSet["file"] {
		dto.ContentFile = ""
	}
	if !fieldSet["type"] {
		dto.Type = ""
	}
	if !fieldSet["tags"] {
		dto.Tags = ""
	}
	if !fieldSet["link"] {
		dto.Link = ""
	}
	if !fieldSet["date"] && !fieldSet["created_at"] && !fieldSet["updated_at"] {
		dto.Date = ""
	}
	if !fieldSet["score"] {
		dto.Score = nil
	}
	if !fieldSet["accent_color"] && !fieldSet["color"] {
		dto.AccentColor = ""
	}
	if !fieldSet["background"] {
		dto.Background = ""
	}
	if !fieldSet["visible"] {
		dto.Visible = nil
	} else if dto.Visible == nil {
		vis := t.Visible
		dto.Visible = &vis
	}
	if !fieldSet["secret"] {
		dto.Secret = ""
	} else if dto.Secret == "" {
		dto.Secret = t.Secret
	}
	if !fieldSet["sort_order"] {
		dto.SortOrder = 0
	} else if dto.SortOrder == 0 {
		dto.SortOrder = t.SortOrder
	}
}

