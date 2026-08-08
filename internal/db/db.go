package db

import (
	"database/sql"
	"fmt"
	"log"
	"strconv"
	"strings"

	_ "github.com/lib/pq"
	"github.com/windowsfreak/leben/internal/config"
	"github.com/windowsfreak/leben/internal/models"
)

type DB struct {
	*sql.DB
}

func Init(cfg *config.Config) (*DB, error) {
	connStr := fmt.Sprintf(
		"host=%s port=%d user=%s password=%s dbname=%s sslmode=%s",
		cfg.Database.Host,
		cfg.Database.Port,
		cfg.Database.User,
		cfg.Database.Password,
		cfg.Database.DBName,
		cfg.Database.SSLMode,
	)

	sqlDB, err := sql.Open("postgres", connStr)
	if err != nil {
		return nil, fmt.Errorf("database connection error: %w", err)
	}

	if err := sqlDB.Ping(); err != nil {
		return nil, fmt.Errorf("database ping error: %w", err)
	}

	database := &DB{DB: sqlDB}

	if err := database.Migrate(); err != nil {
		return nil, fmt.Errorf("database migration failed: %w", err)
	}

	return database, nil
}

func (db *DB) Migrate() error {
	log.Println("Ensuring pgvector extension...")
	if _, err := db.Exec("CREATE EXTENSION IF NOT EXISTS vector;"); err != nil {
		return fmt.Errorf("failed to enable pgvector extension: %w", err)
	}

	log.Println("Ensuring 'tiles' table...")
	query := `
		CREATE TABLE IF NOT EXISTS tiles (
			id SERIAL PRIMARY KEY,
			name VARCHAR(100) NOT NULL,
			language VARCHAR(10) NOT NULL,
			tags TEXT[] NOT NULL,
			title VARCHAR(255) NOT NULL,
			html_teaser TEXT NOT NULL,
			summary TEXT NOT NULL,
			link VARCHAR(1024),
			type VARCHAR(50) NOT NULL DEFAULT 'doc',
			content_file VARCHAR(255),
			visible BOOLEAN NOT NULL DEFAULT TRUE,
			secret VARCHAR(255) NOT NULL DEFAULT '',
			accent_color VARCHAR(50) NOT NULL DEFAULT '#fbbf24',
			background TEXT,
			embedding vector(768),
			sort_order INT NOT NULL DEFAULT 100,
			created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
			CONSTRAINT unique_name_language UNIQUE (name, language)
		);
	`
	if _, err := db.Exec(query); err != nil {
		return fmt.Errorf("failed to create tiles table: %w", err)
	}

	// Ensure secret column exists if upgrading from older schema
	_, _ = db.Exec("ALTER TABLE tiles ADD COLUMN IF NOT EXISTS secret VARCHAR(255) NOT NULL DEFAULT '';")

	log.Println("Ensuring HNSW vector index...")
	if _, err := db.Exec("CREATE INDEX IF NOT EXISTS tiles_embedding_hnsw_idx ON tiles USING hnsw (embedding vector_cosine_ops);"); err != nil {
		log.Printf("Warning: HNSW index creation issue (non-fatal): %v", err)
	}

	return nil
}

// Helpers for PostgreSQL array and vector string conversions

func VectorToString(v []float64) string {
	if len(v) == 0 {
		return "[0" + strings.Repeat(",0", 767) + "]"
	}
	strs := make([]string, len(v))
	for i, val := range v {
		strs[i] = strconv.FormatFloat(val, 'f', -1, 64)
	}
	return "[" + strings.Join(strs, ",") + "]"
}

func StringToVector(s string) []float64 {
	s = strings.Trim(s, "[]")
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	res := make([]float64, 0, len(parts))
	for _, p := range parts {
		f, err := strconv.ParseFloat(strings.TrimSpace(p), 64)
		if err == nil {
			res = append(res, f)
		}
	}
	return res
}

func TagsToPostgres(tagsStr string) string {
	parts := strings.Split(tagsStr, ",")
	cleaned := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			cleaned = append(cleaned, p)
		}
	}
	return "{" + strings.Join(cleaned, ",") + "}"
}

func PostgresToTags(s string) string {
	s = strings.Trim(s, "{}")
	if s == "" {
		return ""
	}
	parts := strings.Split(s, ",")
	cleaned := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			cleaned = append(cleaned, p)
		}
	}
	return strings.Join(cleaned, ", ")
}

func ScanTile(scanner interface{ Scan(dest ...any) error }) (*models.Tile, error) {
	var tile models.Tile
	var tagsStr, vecStr sql.NullString
	var link, contentFile, background sql.NullString

	err := scanner.Scan(
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
	)
	if err != nil {
		return nil, err
	}

	if tagsStr.Valid {
		tile.Tags = PostgresToTags(tagsStr.String)
	} else {
		tile.Tags = ""
	}
	if vecStr.Valid {
		tile.Embedding = StringToVector(vecStr.String)
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

	return &tile, nil
}
