package models

import (
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
	ID          int      `json:"id,omitempty" toon:"id,omitempty"`
	Name        string   `json:"name" toon:"name"`
	Lang        string   `json:"lang,omitempty" toon:"lang,omitempty"`
	Title       string   `json:"title" toon:"title"`
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
