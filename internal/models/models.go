package models

import (
	"fmt"
	"strings"
	"time"
)

type Tile struct {
	ID          int       `json:"id"`
	Name        string    `json:"name"`
	Language    string    `json:"lang"`
	Tags        string    `json:"tags"`
	Title       string    `json:"title"`
	HTMLTeaser  string    `json:"html_teaser"`
	Summary     string    `json:"summary"`
	Link        string    `json:"link,omitempty"`
	Type        string    `json:"type"`
	ContentFile string    `json:"content_file,omitempty"`
	Visible     bool      `json:"visible"`
	Secret      string    `json:"secret,omitempty"`
	AccentColor string    `json:"accent_color"`
	Background  string    `json:"background,omitempty"`
	Embedding   []float64 `json:"-"`
	SortOrder   int       `json:"sort_order"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
	Distance    float64   `json:"-"`
	Score       *float64  `json:"score,omitempty"`
}

type TileDTO struct {
	Index       int      `json:"index,omitempty" toon:"index,omitempty"`
	ID          int      `json:"-" toon:"-"`
	Name        string   `json:"name,omitempty" toon:"name,omitempty"`
	Lang        string   `json:"lang,omitempty" toon:"lang,omitempty"`
	Title       string   `json:"title,omitempty" toon:"title,omitempty"`
	HTMLTeaser  string   `json:"html_teaser,omitempty" toon:"html_teaser,omitempty"`
	Summary     string   `json:"summary,omitempty" toon:"summary,omitempty"`
	Content     string   `json:"content,omitempty" toon:"content,omitempty"`
	ContentFile string   `json:"content_file,omitempty" toon:"content_file,omitempty"`
	Type        string   `json:"type,omitempty" toon:"type,omitempty"`
	Tags        string   `json:"tags,omitempty" toon:"tags,omitempty"`
	Link        string   `json:"link,omitempty" toon:"link,omitempty"`
	Date        string   `json:"date,omitempty" toon:"date,omitempty"`
	Score       *float64 `json:"score,omitempty" toon:"score,omitempty"`
	AccentColor string   `json:"accent_color,omitempty" toon:"accent_color,omitempty"`
	Background  string   `json:"background,omitempty" toon:"background,omitempty"`
	Visible     *bool    `json:"visible,omitempty" toon:"visible,omitempty"`
	Secret      string   `json:"secret,omitempty" toon:"secret,omitempty"`
	SortOrder   int      `json:"sort_order,omitempty" toon:"sort_order,omitempty"`
}

type TileSummary struct {
	ID          int       `json:"id"`
	Name        string    `json:"name"`
	Lang        string    `json:"lang"`
	Title       string    `json:"title"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
	Visible     bool      `json:"visible"`
	ContentFile string    `json:"content_file,omitempty"`
}

type TranslationStatusItem struct {
	ID          int        `json:"id"`
	Language    string     `json:"language"`
	IsSource    bool       `json:"is_source"`
	Status      string     `json:"status"` // source, up_to_date, stale
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
	ContentFile string     `json:"content_file,omitempty"`
	FileMTime   *time.Time `json:"file_mtime,omitempty"`
}

type TileTranslationMatrix struct {
	Name                 string                           `json:"name"`
	SourceLanguage       string                           `json:"source_language"`
	EffectiveSourceMTime time.Time                        `json:"effective_source_mtime"`
	HasStaleTranslation  bool                             `json:"has_stale_translation"`
	Languages            map[string]TranslationStatusItem `json:"languages"`
	MissingLanguages     []string                         `json:"missing_languages"`
}

type TaskStatus string

const (
	TaskPending   TaskStatus = "pending"
	TaskRunning   TaskStatus = "running"
	TaskCompleted TaskStatus = "completed"
	TaskFailed    TaskStatus = "failed"
	TaskCancelled TaskStatus = "cancelled"
)

type TranslationTask struct {
	ID          string     `json:"id"`
	TileName    string     `json:"tile_name"`
	TargetLang  string     `json:"target_lang"`
	Status      TaskStatus `json:"status"`
	Progress    string     `json:"progress"`
	Error       string     `json:"error,omitempty"`
	StartedAt   time.Time  `json:"started_at"`
	CompletedAt *time.Time `json:"completed_at,omitempty"`
	Result      any        `json:"result,omitempty"`
}

type ApiToken struct {
	ID         int        `json:"id"`
	Kind       string     `json:"kind"` // "session" (browser cookie) or "device" (bearer token)
	Name       string     `json:"name"`
	UserAgent  string     `json:"user_agent"`
	IP         string     `json:"ip"`
	CreatedAt  time.Time  `json:"created_at"`
	LastUsedAt *time.Time `json:"last_used_at"`
	ExpiresAt  *time.Time `json:"expires_at"`
	RevokedAt  *time.Time `json:"revoked_at"`
}

type ApiTokenView struct {
	ApiToken
	Current bool `json:"current"`
}

type MCPTool struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	InputSchema any    `json:"inputSchema"`
}

type MCPRequest struct {
	JSONRPC string         `json:"jsonrpc"`
	ID      any            `json:"id"`
	Method  string         `json:"method"`
	Params  map[string]any `json:"params"`
}

type MCPResponse struct {
	JSONRPC string `json:"jsonrpc"`
	ID      any    `json:"id,omitempty"`
	Result  any    `json:"result,omitempty"`
	Error   any    `json:"error,omitempty"`
}

type TilePatchDTO struct {
	Name        string  `json:"name"`
	Lang        string  `json:"lang,omitempty"`
	Language    string  `json:"language,omitempty"`
	Tile        string  `json:"tile,omitempty"` // compound alias e.g. "finance:de"
	NewName     *string `json:"new_name,omitempty"`
	Title       *string `json:"title,omitempty"`
	HTMLTeaser  *string `json:"html_teaser,omitempty"`
	Summary     *string `json:"summary,omitempty"`
	ContentFile *string `json:"content_file,omitempty"`
	Tags        *string `json:"tags,omitempty"`
	Type        *string `json:"type,omitempty"`
	Link        *string `json:"link,omitempty"`
	Secret      *string `json:"secret,omitempty"`
	AccentColor *string `json:"accent_color,omitempty"`
	Background  *string `json:"background,omitempty"`
	Visible     *bool   `json:"visible,omitempty"`
	SortOrder   *int    `json:"sort_order,omitempty"`
}

func (p *TilePatchDTO) Normalize() (string, string, error) {
	name := strings.TrimSpace(p.Name)
	if name == "" {
		name = strings.TrimSpace(p.Tile)
	}
	lang := strings.ToLower(strings.TrimSpace(p.Lang))
	if lang == "" {
		lang = strings.ToLower(strings.TrimSpace(p.Language))
	}

	if idx := strings.Index(name, ":"); idx != -1 {
		embeddedName := strings.TrimSpace(name[:idx])
		embeddedLang := strings.ToLower(strings.TrimSpace(name[idx+1:]))
		if embeddedLang != "" {
			lang = embeddedLang
			name = embeddedName
		}
	}

	if name == "" {
		return "", "", fmt.Errorf("tile name is required")
	}
	if lang == "" {
		return "", "", fmt.Errorf("tile language is required (or use 'name:lang' syntax)")
	}
	return strings.ToLower(name), lang, nil
}

type TilePatchItemResult struct {
	Name    string `json:"name"`
	Lang    string `json:"lang"`
	Status  string `json:"status"` // "ok" or "error"
	NewName string `json:"new_name,omitempty"`
	Error   string `json:"error,omitempty"`
}

type BatchPatchResult struct {
	Status    string                `json:"status"` // "success" or "partial_success" or "error"
	Total     int                   `json:"total"`
	Succeeded int                   `json:"succeeded"`
	Failed    int                   `json:"failed"`
	Errors    []string              `json:"errors,omitempty"`
	Results   []TilePatchItemResult `json:"results"`
}

type TileSpec struct {
	Name string `json:"name"`
	Lang string `json:"lang"`
}

