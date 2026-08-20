package services

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/windowsfreak/leben/internal/config"
	"github.com/windowsfreak/leben/internal/db"
	"github.com/windowsfreak/leben/internal/models"
)

type TileService struct {
	cfg    *config.Config
	database *db.DB
	ollama *OllamaService
}

func NewTileService(cfg *config.Config, database *db.DB, ollama *OllamaService) *TileService {
	return &TileService{
		cfg:    cfg,
		database: database,
		ollama: ollama,
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

		sqlQuery := `
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
			ORDER BY distance ASC 
			LIMIT $5 OFFSET $6
		`
		rows, err := s.database.QueryContext(ctx, sqlQuery, vecStr, prefLang, showInvisible, refCodesStr, dbLimit, offset)
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
			if !showInvisible {
				tile.Secret = ""
			}
			results = append(results, &tile)
		}
		return results, nil
	}

	// No query: sort by sort_order
	sqlQuery := `
		WITH resolved_tiles AS (
			SELECT *,
				   ROW_NUMBER() OVER (
					   PARTITION BY name 
					   ORDER BY 
						   CASE WHEN language = $1 THEN 1 
								ELSE 2 
						   END,
						   sort_order ASC, created_at DESC
				   ) as rn
			FROM tiles
			WHERE ($2 = true OR (visible = true AND (secret = '' OR secret = ANY(string_to_array($3, ',')))))
		)
		SELECT id, name, language, tags, title, html_teaser, 
			   summary, link, type, content_file, 
			   visible, secret, accent_color, background, embedding, sort_order, created_at, updated_at
		FROM resolved_tiles 
		WHERE rn = 1 
		ORDER BY sort_order ASC, created_at DESC
		LIMIT $4 OFFSET $5
	`
	rows, err := s.database.QueryContext(ctx, sqlQuery, prefLang, showInvisible, refCodesStr, dbLimit, offset)
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
		ORDER BY distance ASC 
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
		if !showInvisible {
			tile.Secret = ""
		}
		results = append(results, &tile)
	}
	return results, nil
}

func (s *TileService) GetTile(ctx context.Context, name, lang string, refCodes []string, showInvisible bool) (*models.Tile, error) {
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

	// Fallback to alternative language if requested not found
	if err == sql.ErrNoRows {
		fallback := "de"
		if lang == "de" {
			fallback = "en"
		}
		row = s.database.QueryRowContext(ctx, sqlQuery, name, fallback, showInvisible, refCodesStr)
		tile, err = db.ScanTile(row)
	}

	if err != nil {
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
		if err := rows.Scan(&s.ID, &s.Name, &s.Language, &s.Title, &s.CreatedAt, &s.UpdatedAt, &s.Visible, &cf); err != nil {
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
	// Generate embedding if necessary
	docText := FormatTileDocumentText(tile.Name, tile.Language, tile.Tags, tile.Summary)
	vec, err := s.ollama.GetEmbedding(ctx, docText, "document")
	if err != nil {
		// Zero vector fallback
		vec = make([]float64, 768)
	}
	vecStr := db.VectorToString(vec)
	pgTags := db.TagsToPostgres(tile.Tags)

	if tile.SortOrder == 0 {
		tile.SortOrder = 100
	}
	if tile.AccentColor == "" {
		tile.AccentColor = "#fbbf24"
	}
	if tile.Type == "" {
		tile.Type = "doc"
	}

	if tile.ID > 0 {
		sqlQuery := `
			UPDATE tiles 
			SET name = $1, language = $2, tags = $3, title = $4, html_teaser = $5, summary = $6,
				link = $7, type = $8, content_file = $9, visible = $10, secret = $11, accent_color = $12,
				background = $13, embedding = $14::vector, sort_order = $15, updated_at = CURRENT_TIMESTAMP
			WHERE id = $16
		`
		_, err := s.database.ExecContext(ctx, sqlQuery,
			tile.Name, tile.Language, pgTags, tile.Title, tile.HTMLTeaser, tile.Summary,
			tile.Link, tile.Type, tile.ContentFile, tile.Visible, tile.Secret, tile.AccentColor,
			tile.Background, vecStr, tile.SortOrder, tile.ID,
		)
		return err
	}

	sqlQuery := `
		INSERT INTO tiles (
			name, language, tags, title, html_teaser, summary, link, type, content_file,
			visible, secret, accent_color, background, embedding, sort_order, created_at, updated_at
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14::vector, $15, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP
		) RETURNING id
	`
	return s.database.QueryRowContext(ctx, sqlQuery,
		tile.Name, tile.Language, pgTags, tile.Title, tile.HTMLTeaser, tile.Summary,
		tile.Link, tile.Type, tile.ContentFile, tile.Visible, tile.Secret, tile.AccentColor,
		tile.Background, vecStr, tile.SortOrder,
	).Scan(&tile.ID)
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
		sourceRow := siblings[0]
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

