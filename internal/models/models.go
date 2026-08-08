package models

import (
	"time"
)

type Tile struct {
	ID          int       `json:"id"`
	Name        string    `json:"name"`
	Language    string    `json:"language"`
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
	Distance    float64   `json:"distance,omitempty"`
}

type TileSummary struct {
	ID          int       `json:"id"`
	Name        string    `json:"name"`
	Language    string    `json:"language"`
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
