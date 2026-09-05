package mcp

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/windowsfreak/leben/internal/auth"
	"github.com/windowsfreak/leben/internal/config"
	"github.com/windowsfreak/leben/internal/models"
	"github.com/windowsfreak/leben/internal/services"
	"github.com/windowsfreak/leben/internal/tasks"
)

type Server struct {
	cfg      *config.Config
	auth     *auth.Auth
	tileSvc  *services.TileService
	transSvc *services.TranslationService
	llmSvc   *services.LLMService
	taskMgr  *tasks.Manager
	sessions sync.Map // sid -> chan string
}

func NewServer(
	cfg *config.Config,
	authModule *auth.Auth,
	tileSvc *services.TileService,
	transSvc *services.TranslationService,
	llmSvc *services.LLMService,
	taskMgr *tasks.Manager,
) *Server {
	return &Server{
		cfg:      cfg,
		auth:     authModule,
		tileSvc:  tileSvc,
		transSvc: transSvc,
		llmSvc:   llmSvc,
		taskMgr:  taskMgr,
	}
}

func (s *Server) GetTools(isAdmin bool) []models.MCPTool {
	langs := []string{"de", "en", "fr", "es", "pl", "ro", "sq", "tr", "ru", "uk", "ar"}
	targetLangs := append(langs, "all")

	publicTools := []models.MCPTool{
		{
			Name:        "search_tiles",
			Description: "Semantic vector search across Björn's profile cards ('tiles'). Use `q` for a natural-language query, `similar` for find-similar-by-card-name, or omit both to list all cards in curated order.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"q":       map[string]any{"type": "string", "description": "Natural language query or keywords"},
					"similar": map[string]any{"type": "string", "description": "Card name to find semantically similar cards for"},
					"lang":    map[string]any{"type": "string", "enum": langs, "description": "Preferred language (default 'de')"},
					"limit":   map[string]any{"type": "integer", "description": "Maximum tiles to return (default 20, 0 = all)"},
					"offset":  map[string]any{"type": "integer", "description": "Pagination offset (default 0)"},
				},
			},
		},
		{
			Name:        "get_similar_tiles",
			Description: "Find tiles semantically similar to an existing card by name.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"name":   map[string]any{"type": "string", "description": "Name of the source tile"},
					"lang":   map[string]any{"type": "string", "enum": langs, "description": "Preferred language (default 'de')"},
					"limit":  map[string]any{"type": "integer", "description": "Maximum tiles to return (default 5)"},
					"offset": map[string]any{"type": "integer", "description": "Pagination offset (default 0)"},
				},
				"required": []string{"name"},
			},
		},
		{
			Name:        "get_tile",
			Description: "Fetch full metadata and optional detailed article content for a specific card by name and language.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"name":            map[string]any{"type": "string", "description": "Card name, e.g. 'finance'"},
					"lang":            map[string]any{"type": "string", "enum": langs, "description": "Language code (default 'de')"},
					"include_content": map[string]any{"type": "boolean", "description": "Whether to include full HTML content from file"},
				},
				"required": []string{"name"},
			},
		},
		{
			Name:        "get_tile_versions",
			Description: "List all existing language versions of a card.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"name": map[string]any{"type": "string", "description": "Name of the card"},
				},
				"required": []string{"name"},
			},
		},
		{
			Name:        "check_auth",
			Description: "Report whether authentication works and what role is active (anonymous vs admin).",
			InputSchema: map[string]any{
				"type":       "object",
				"properties": map[string]any{},
			},
		},
	}

	if !isAdmin {
		return publicTools
	}

	adminTools := []models.MCPTool{
		{
			Name:        "admin_list_tiles",
			Description: "List every card in the database, including invisible and secret ones.",
			InputSchema: map[string]any{
				"type":       "object",
				"properties": map[string]any{},
			},
		},
		{
			Name:        "admin_save_tile",
			Description: "Create a card (no id) or update an existing one (id > 0). Regenerates the semantic embedding. One card exists per (name, language) pair.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"id":           map[string]any{"type": "integer", "description": "Existing card id to update; omit to create"},
					"name":         map[string]any{"type": "string", "description": "Unique card name (slug), e.g. 'finance'"},
					"language":     map[string]any{"type": "string", "description": "Language code (default 'de')"},
					"title":        map[string]any{"type": "string", "description": "Display title"},
					"summary":      map[string]any{"type": "string", "description": "High-level summary used for search embeddings and snippets"},
					"html_teaser":  map[string]any{"type": "string", "description": "HTML teaser shown on the card front"},
					"content_file": map[string]any{"type": "string", "description": "Filename of the HTML article under /content/, e.g. 'finance_de.html'"},
					"tags":         map[string]any{"type": "string", "description": "Comma-delimited tags"},
					"type":         map[string]any{"type": "string", "enum": []string{"doc", "link"}, "description": "Card type (default 'doc')"},
					"link":         map[string]any{"type": "string", "description": "External URL when type=link"},
					"secret":       map[string]any{"type": "string", "description": "Reference code required to unlock this card ('' = public)"},
					"accent_color": map[string]any{"type": "string", "description": "Accent color, e.g. '#fbbf24'"},
					"background":   map[string]any{"type": "string", "description": "Background image path or CSS"},
					"visible":      map[string]any{"type": "boolean", "description": "Visible on the public site (default true)"},
					"sort_order":   map[string]any{"type": "integer", "description": "Curated order (default 100)"},
				},
				"required": []string{"name", "title"},
			},
		},
		{
			Name:        "admin_update_tile_fields",
			Description: "Partially update one or more fields on an existing tile without overwriting unmentioned fields. Identify by numeric `id`, or by `name` (+ optional `language`). Regenerates embedding.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"id":           map[string]any{"type": "integer", "description": "Numeric tile ID to update (either id or name must be provided)"},
					"name":         map[string]any{"type": "string", "description": "Card unique name / slug. Required if id is omitted."},
					"language":     map[string]any{"type": "string", "description": "Language code, e.g. 'de' or 'en' (default 'de')"},
					"title":        map[string]any{"type": "string", "description": "Display title"},
					"summary":      map[string]any{"type": "string", "description": "High-level summary"},
					"html_teaser":  map[string]any{"type": "string", "description": "HTML shown on the card front"},
					"content_file": map[string]any{"type": "string", "description": "Filename of the HTML article under /content/"},
					"tags":         map[string]any{"type": "string", "description": "Comma-delimited tags"},
					"type":         map[string]any{"type": "string", "enum": []string{"doc", "link"}, "description": "Card type"},
					"link":         map[string]any{"type": "string", "description": "External URL when type=link"},
					"secret":       map[string]any{"type": "string", "description": "Reference code required to unlock"},
					"accent_color": map[string]any{"type": "string", "description": "Accent color"},
					"background":   map[string]any{"type": "string", "description": "Background image path or CSS"},
					"visible":      map[string]any{"type": "boolean", "description": "Visible on the public site"},
					"sort_order":   map[string]any{"type": "integer", "description": "Curated order"},
				},
			},
		},
		{
			Name:        "admin_delete_tile",
			Description: "Permanently delete a card by numeric id (deletes one (name, language) version).",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"id": map[string]any{"type": "integer", "description": "Card id"},
				},
				"required": []string{"id"},
			},
		},
		{
			Name:        "admin_toggle_visibility",
			Description: "Show or hide a card on the public site.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"id": map[string]any{"type": "integer", "description": "Card id"},
				},
				"required": []string{"id"},
			},
		},
		{
			Name:        "admin_clone_tile",
			Description: "Duplicate a card as '<name>-copy' (same language).",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"id": map[string]any{"type": "integer", "description": "Card id"},
				},
				"required": []string{"id"},
			},
		},
		{
			Name:        "translate_tile",
			Description: "Start an asynchronous translation task (DeepL first, LLM fallback) for a card's metadata, teaser and content file. Poll `list_tasks` for progress.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"name":        map[string]any{"type": "string", "description": "Name of the tile to translate"},
					"target_lang": map[string]any{"type": "string", "enum": targetLangs, "description": "Target language code or 'all'"},
				},
				"required": []string{"name"},
			},
		},
		{
			Name:        "translation_status",
			Description: "Per-card translation freshness matrix (source / up_to_date / stale / missing).",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"name": map[string]any{"type": "string", "description": "Filter by card name (optional)"},
				},
			},
		},
		{
			Name:        "refresh_vectors",
			Description: "Re-generate semantic embeddings for all cards (run after bulk changes or Ollama outages).",
			InputSchema: map[string]any{
				"type":       "object",
				"properties": map[string]any{},
			},
		},
		{
			Name:        "list_tasks",
			Description: "List translation/background tasks, or fetch one by id. Fields: id, tile_name, target_lang, status, progress, error.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"id": map[string]any{"type": "string", "description": "Task id to fetch a single task (optional)"},
				},
			},
		},
		{
			Name:        "cancel_task",
			Description: "Cancel a running background translation task by task ID.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"id": map[string]any{"type": "string", "description": "Task ID to cancel"},
				},
				"required": []string{"id"},
			},
		},
		{
			Name:        "get_content_file",
			Description: "Read an HTML article file from /content/ (e.g. 'finance_de.html'). Returns content and its mtime.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"file": map[string]any{"type": "string", "description": "Filename, e.g. 'finance_de.html'"},
				},
				"required": []string{"file"},
			},
		},
		{
			Name:        "save_content_file",
			Description: "Create or update an HTML article file in /content/. Supports optimistic locking with expected_mtime.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"file":           map[string]any{"type": "string", "description": "Filename (e.g. 'contact_de.html')"},
					"content":        map[string]any{"type": "string", "description": "Full HTML content"},
					"expected_mtime": map[string]any{"type": "integer", "description": "mtime from a previous get_content_file call for conflict detection (optional)"},
				},
				"required": []string{"file", "content"},
			},
		},
		{
			Name:        "suggest_meta",
			Description: "Have the server-side LLM suggest a summary and tags for a card from its content.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"name":         map[string]any{"type": "string", "description": "Card name"},
					"title":        map[string]any{"type": "string", "description": "Card title"},
					"content_file": map[string]any{"type": "string", "description": "Content filename to analyze (optional)"},
					"html_teaser":  map[string]any{"type": "string", "description": "HTML teaser to include (optional)"},
				},
				"required": []string{"name", "title"},
			},
		},
		{
			Name:        "edit_html_teaser",
			Description: "Apply a natural-language editing instruction to a card's HTML teaser via the server-side LLM.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"prompt":      map[string]any{"type": "string", "description": "Editing instruction, e.g. 'make it shorter and friendlier'"},
					"html_teaser": map[string]any{"type": "string", "description": "Current HTML teaser"},
				},
				"required": []string{"prompt", "html_teaser"},
			},
		},
		{
			Name:        "get_frontend_config",
			Description: "Read the public frontend configuration (frontend/config.json).",
			InputSchema: map[string]any{
				"type":       "object",
				"properties": map[string]any{},
			},
		},
		{
			Name:        "save_frontend_config",
			Description: "Overwrite frontend/config.json with the given JSON string (must be valid JSON).",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"config_json": map[string]any{"type": "string", "description": "Complete config.json content as a JSON string"},
				},
				"required": []string{"config_json"},
			},
		},
		{
			Name:        "manage_media",
			Description: "List, upload, rename or delete files. kind=image targets /tileimg/, kind=asset targets /assets/.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"kind":     map[string]any{"type": "string", "enum": []string{"image", "asset"}, "description": "Which media directory to operate on"},
					"action":   map[string]any{"type": "string", "enum": []string{"list", "upload", "rename", "delete"}, "description": "Operation to perform"},
					"name":     map[string]any{"type": "string", "description": "Filename for delete, rename, or upload"},
					"new_name": map[string]any{"type": "string", "description": "New filename for rename"},
					"path":     map[string]any{"type": "string", "description": "Local file path to read from (for upload)"},
					"data":     map[string]any{"type": "string", "description": "Base64 or raw string file content (for upload)"},
				},
				"required": []string{"kind", "action"},
			},
		},
		{
			Name:        "manage_api_tokens",
			Description: "List issued API tokens (with last-used timestamps) or revoke one by numeric id.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"action": map[string]any{"type": "string", "enum": []string{"list", "revoke"}, "description": "Operation"},
					"id":     map[string]any{"type": "integer", "description": "Token id to revoke (required when action=revoke)"},
				},
				"required": []string{"action"},
			},
		},
	}

	return append(publicTools, adminTools...)
}

// HandleSSE manages SSE (Server-Sent Events) streaming connection (legacy & notification channel)
func (s *Server) HandleSSE(w http.ResponseWriter, r *http.Request, baseURL string) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming unsupported", http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	sessionID := r.Header.Get("Mcp-Session-Id")
	if sessionID == "" {
		sessionID = r.URL.Query().Get("sessionId")
	}
	if sessionID == "" {
		sessionID = uuid.New().String()
	}
	w.Header().Set("Mcp-Session-Id", sessionID)

	msgChan := make(chan string, 10)
	s.sessions.Store(sessionID, msgChan)
	defer s.sessions.Delete(sessionID)

	base := strings.TrimRight(baseURL, "/")
	if base == "" {
		scheme := "http"
		if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
			scheme = "https"
		}
		base = fmt.Sprintf("%s://%s", scheme, r.Host)
	}
	messageURL := fmt.Sprintf("%s/api/mcp?action=message&sessionId=%s", base, sessionID)

	fmt.Fprintf(w, "event: endpoint\ndata: %s\n\n", messageURL)
	flusher.Flush()

	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case msg := <-msgChan:
			fmt.Fprintf(w, "event: message\ndata: %s\n\n", msg)
			flusher.Flush()
		case <-ticker.C:
			fmt.Fprintf(w, ": keepalive\n\n")
			flusher.Flush()
		}
	}
}

// HandleMessage manages modern Streamable HTTP and HTTP POST JSON-RPC 2.0 calls
func (s *Server) HandleMessage(w http.ResponseWriter, r *http.Request, isAdmin bool, baseURL string) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Expose-Headers", "Mcp-Session-Id, WWW-Authenticate")

	sessionID := r.Header.Get("Mcp-Session-Id")
	if sessionID == "" {
		sessionID = r.URL.Query().Get("sessionId")
	}
	if sessionID == "" {
		sessionID = uuid.New().String()
	}
	w.Header().Set("Mcp-Session-Id", sessionID)

	if !isAdmin {
		w.Header().Set("WWW-Authenticate", fmt.Sprintf(`Bearer resource_metadata="%s/.well-known/oauth-protected-resource/api/mcp"`, strings.TrimRight(baseURL, "/")))
	}

	var req models.MCPRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(models.MCPResponse{
			JSONRPC: "2.0",
			Error:   map[string]any{"code": -32700, "message": "Parse error"},
		})
		return
	}

	// Notifications (e.g. notifications/initialized) don't return a JSON-RPC response
	if req.Method == "notifications/initialized" {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	resp := s.ExecuteMethod(r.Context(), req, isAdmin)

	// Send to session SSE queue if an SSE listener exists for this session
	if chVal, ok := s.sessions.Load(sessionID); ok {
		msgChan := chVal.(chan string)
		b, _ := json.Marshal(resp)
		select {
		case msgChan <- string(b):
		default:
		}
	}

	// Streamable HTTP: if client requested text/event-stream on POST, stream response as SSE
	if strings.Contains(r.Header.Get("Accept"), "text/event-stream") {
		if flusher, ok := w.(http.Flusher); ok {
			w.Header().Set("Content-Type", "text/event-stream")
			w.Header().Set("Cache-Control", "no-cache")
			w.Header().Set("Connection", "keep-alive")
			b, _ := json.Marshal(resp)
			fmt.Fprintf(w, "event: message\ndata: %s\n\n", string(b))
			flusher.Flush()
			return
		}
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(resp)
}

func (s *Server) ExecuteMethod(ctx context.Context, req models.MCPRequest, isAdmin bool) models.MCPResponse {
	switch req.Method {
	case "initialize":
		return models.MCPResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result: map[string]any{
				"protocolVersion": "2024-11-05",
				"capabilities": map[string]any{
					"tools": map[string]any{
						"listChanged": false,
					},
				},
				"serverInfo": map[string]any{
					"name":    "leben-mcp",
					"version": "1.0.0",
				},
				"instructions": "Semantic vector search and profile cards for Björn Eberhardt (leben.8bj.de).",
			},
		}

	case "ping":
		return models.MCPResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result:  map[string]any{},
		}

	case "tools/list":
		return models.MCPResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result: map[string]any{
				"tools": s.GetTools(isAdmin),
			},
		}

	case "tools/call":
		name, _ := req.Params["name"].(string)
		args, _ := req.Params["arguments"].(map[string]any)

		res, err := s.ExecuteTool(ctx, name, args, isAdmin)
		if err != nil {
			return models.MCPResponse{
				JSONRPC: "2.0",
				ID:      req.ID,
				Result: map[string]any{
					"content": []map[string]any{
						{"type": "text", "text": fmt.Sprintf("Error: %v", err)},
					},
					"isError": true,
				},
			}
		}

		resBytes, _ := json.MarshalIndent(res, "", "  ")
		return models.MCPResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result: map[string]any{
				"content": []map[string]any{
					{"type": "text", "text": string(resBytes)},
				},
			},
		}

	default:
		return models.MCPResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error:   map[string]any{"code": -32601, "message": "Method not found"},
		}
	}
}

func (s *Server) ExecuteTool(ctx context.Context, name string, args map[string]any, isAdmin bool) (any, error) {
	if args == nil {
		args = make(map[string]any)
	}

	adminOnlyTools := map[string]bool{
		"admin_list_tiles":         true,
		"admin_save_tile":          true,
		"admin_update_tile_fields": true,
		"admin_delete_tile":        true,
		"admin_toggle_visibility":  true,
		"admin_clone_tile":         true,
		"translate_tile":           true,
		"translation_status":       true,
		"refresh_vectors":          true,
		"list_tasks":               true,
		"cancel_task":              true,
		"get_content_file":         true,
		"save_content_file":        true,
		"suggest_meta":             true,
		"edit_html_teaser":         true,
		"get_frontend_config":      true,
		"save_frontend_config":     true,
		"manage_media":             true,
		"manage_api_tokens":        true,
	}

	if adminOnlyTools[name] && !isAdmin {
		return nil, fmt.Errorf("unauthorized: tool '%s' requires admin authentication", name)
	}

	switch name {
	case "search_tiles":
		q := getString(args, "q")
		similar := getString(args, "similar")
		lang := getString(args, "lang")
		if lang == "" {
			lang = "de"
		}
		limit := getInt(args, "limit")
		if limit <= 0 {
			limit = 20
		}
		offset := getInt(args, "offset")

		if similar != "" {
			return s.tileSvc.GetSimilarTiles(ctx, similar, lang, nil, isAdmin, limit, offset)
		}
		return s.tileSvc.SearchTiles(ctx, lang, q, nil, isAdmin, offset, limit)

	case "get_similar_tiles":
		tName := getString(args, "name")
		lang := getString(args, "lang")
		if lang == "" {
			lang = "de"
		}
		limit := getInt(args, "limit")
		if limit <= 0 {
			limit = 5
		}
		offset := getInt(args, "offset")
		return s.tileSvc.GetSimilarTiles(ctx, tName, lang, nil, isAdmin, limit, offset)

	case "get_tile":
		tName := getString(args, "name")
		lang := getString(args, "lang")
		if lang == "" {
			lang = "de"
		}
		tile, err := s.tileSvc.GetTile(ctx, tName, lang, nil, isAdmin)
		if err != nil {
			return nil, err
		}
		if getBool(args, "include_content", false) && tile.ContentFile != "" {
			fPath := filepath.Join(s.cfg.Server.WebDir, "content", filepath.Clean(tile.ContentFile))
			if b, readErr := os.ReadFile(fPath); readErr == nil {
				return map[string]any{
					"tile":    tile,
					"content": string(b),
				}, nil
			}
		}
		return tile, nil

	case "get_tile_versions":
		tName := getString(args, "name")
		return s.tileSvc.GetTileInfo(ctx, tName, nil, isAdmin)

	case "check_auth":
		if isAdmin {
			return map[string]any{"authenticated": true, "role": "admin"}, nil
		}
		return map[string]any{"authenticated": false, "role": "anonymous"}, nil

	case "admin_list_tiles":
		tiles, err := s.tileSvc.GetAllTiles(ctx)
		if err != nil {
			return nil, err
		}
		return map[string]any{"status": "success", "tiles": tiles}, nil

	case "admin_save_tile":
		tile := models.Tile{
			ID:          getInt(args, "id"),
			Name:        getString(args, "name"),
			Language:    getString(args, "language"),
			Title:       getString(args, "title"),
			Summary:     getString(args, "summary"),
			HTMLTeaser:  getString(args, "html_teaser"),
			ContentFile: getString(args, "content_file"),
			Tags:        getString(args, "tags"),
			Type:        getString(args, "type"),
			Link:        getString(args, "link"),
			Secret:      getString(args, "secret"),
			AccentColor: getString(args, "accent_color"),
			Background:  getString(args, "background"),
			Visible:     getBool(args, "visible", true),
			SortOrder:   getInt(args, "sort_order"),
		}
		if tile.Language == "" {
			tile.Language = getString(args, "lang")
		}
		if tile.Language == "" {
			tile.Language = "de"
		}
		if err := s.tileSvc.SaveTile(ctx, &tile); err != nil {
			return nil, err
		}
		return map[string]any{"status": "success", "message": "Tile saved.", "id": tile.ID}, nil

	case "admin_update_tile_fields":
		id := getInt(args, "id")
		name := getString(args, "name")
		language := getString(args, "language")
		if language == "" {
			language = getString(args, "lang")
		}
		if language == "" {
			language = "de"
		}

		tiles, err := s.tileSvc.GetAllTiles(ctx)
		if err != nil {
			return nil, err
		}

		var existing *models.Tile
		if id > 0 {
			for _, t := range tiles {
				if t.ID == id {
					existing = t
					break
				}
			}
			if existing == nil {
				return nil, fmt.Errorf("tile with id %d not found", id)
			}
		} else if name != "" {
			for _, t := range tiles {
				if t.Name == name && t.Language == language {
					existing = t
					break
				}
			}
			if existing == nil {
				return nil, fmt.Errorf("tile with name '%s' and language '%s' not found", name, language)
			}
		} else {
			return nil, fmt.Errorf("either 'id' or 'name' must be provided")
		}

		if val, ok := args["title"]; ok {
			existing.Title = fmt.Sprintf("%v", val)
		}
		if val, ok := args["summary"]; ok {
			existing.Summary = fmt.Sprintf("%v", val)
		}
		if val, ok := args["html_teaser"]; ok {
			existing.HTMLTeaser = fmt.Sprintf("%v", val)
		}
		if val, ok := args["content_file"]; ok {
			existing.ContentFile = fmt.Sprintf("%v", val)
		}
		if val, ok := args["tags"]; ok {
			existing.Tags = fmt.Sprintf("%v", val)
		}
		if val, ok := args["type"]; ok {
			existing.Type = fmt.Sprintf("%v", val)
		}
		if val, ok := args["link"]; ok {
			existing.Link = fmt.Sprintf("%v", val)
		}
		if val, ok := args["secret"]; ok {
			existing.Secret = fmt.Sprintf("%v", val)
		}
		if val, ok := args["accent_color"]; ok {
			existing.AccentColor = fmt.Sprintf("%v", val)
		}
		if val, ok := args["background"]; ok {
			existing.Background = fmt.Sprintf("%v", val)
		}
		if _, ok := args["visible"]; ok {
			existing.Visible = getBool(args, "visible", true)
		}
		if _, ok := args["sort_order"]; ok {
			existing.SortOrder = getInt(args, "sort_order")
		}
		if id > 0 && name != "" {
			existing.Name = name
		}
		if id > 0 && language != "" {
			existing.Language = language
		}

		if err := s.tileSvc.SaveTile(ctx, existing); err != nil {
			return nil, err
		}
		return map[string]any{"status": "success", "message": "Tile updated.", "tile": existing}, nil

	case "admin_delete_tile":
		id := getInt(args, "id")
		if id <= 0 {
			return nil, fmt.Errorf("id must be a positive integer")
		}
		if err := s.tileSvc.DeleteTile(ctx, id); err != nil {
			return nil, err
		}
		return map[string]any{"status": "success", "message": fmt.Sprintf("Tile %d deleted.", id)}, nil

	case "admin_toggle_visibility":
		id := getInt(args, "id")
		if id <= 0 {
			return nil, fmt.Errorf("id must be a positive integer")
		}
		if err := s.tileSvc.ToggleVisibility(ctx, id); err != nil {
			return nil, err
		}
		return map[string]any{"status": "success", "message": fmt.Sprintf("Visibility toggled for tile %d.", id)}, nil

	case "admin_clone_tile":
		id := getInt(args, "id")
		if id <= 0 {
			return nil, fmt.Errorf("id must be a positive integer")
		}
		cloned, err := s.tileSvc.CloneTile(ctx, id)
		if err != nil {
			return nil, err
		}
		return map[string]any{"status": "success", "message": "Tile cloned successfully.", "tile": cloned}, nil

	case "translate_tile":
		tName := getString(args, "name")
		targetLang := getString(args, "target_lang")
		if targetLang == "" {
			targetLang = "all"
		}
		return s.transSvc.StartAutoTranslateTask(ctx, tName, targetLang)

	case "translation_status":
		name := getString(args, "name")
		matrix, err := s.tileSvc.GetTranslationStatus(ctx, name)
		if err != nil {
			return nil, err
		}
		return map[string]any{"status": "success", "translation_status": matrix}, nil

	case "refresh_vectors":
		count, err := s.tileSvc.RefreshVectors(ctx)
		if err != nil {
			return nil, err
		}
		return map[string]any{"status": "success", "message": fmt.Sprintf("Refreshed embeddings for %d tiles.", count), "refreshed": count}, nil

	case "list_tasks":
		tID := getString(args, "id")
		if tID != "" {
			if task, ok := s.taskMgr.GetTask(tID); ok {
				return task, nil
			}
			return nil, fmt.Errorf("task '%s' not found", tID)
		}
		return s.taskMgr.GetTasks(), nil

	case "cancel_task":
		tID := getString(args, "id")
		if s.taskMgr.CancelTask(tID) {
			return map[string]string{"status": "success", "message": "Task cancelled successfully"}, nil
		}
		return nil, fmt.Errorf("task '%s' not running or not found", tID)

	case "get_content_file":
		file := filepath.Base(getString(args, "file"))
		if file == "" || file == "." {
			return nil, fmt.Errorf("file argument required")
		}
		fPath := filepath.Join(s.cfg.Server.WebDir, "content", file)
		data, err := os.ReadFile(fPath)
		if err != nil {
			return nil, fmt.Errorf("content file '%s' not found: %w", file, err)
		}
		info, _ := os.Stat(fPath)
		mtime := int64(0)
		if info != nil {
			mtime = info.ModTime().Unix()
		}
		return map[string]any{
			"status":  "success",
			"file":    file,
			"content": string(data),
			"mtime":   mtime,
		}, nil

	case "save_content_file":
		file := filepath.Base(getString(args, "file"))
		content := getString(args, "content")
		expectedMtime := int64(getInt(args, "expected_mtime"))
		if file == "" || file == "." {
			return nil, fmt.Errorf("file argument required")
		}
		fPath := filepath.Join(s.cfg.Server.WebDir, "content", file)

		if expectedMtime > 0 {
			if info, err := os.Stat(fPath); err == nil {
				if info.ModTime().Unix() > expectedMtime {
					return nil, fmt.Errorf("conflict: content file '%s' has been modified on server (actual mtime %d > expected %d)", file, info.ModTime().Unix(), expectedMtime)
				}
			}
		}

		if err := os.WriteFile(fPath, []byte(content), 0644); err != nil {
			return nil, err
		}
		info, _ := os.Stat(fPath)
		mtime := int64(0)
		if info != nil {
			mtime = info.ModTime().Unix()
		}
		return map[string]any{"status": "success", "message": "Content file saved", "file": file, "mtime": mtime}, nil

	case "suggest_meta":
		name := getString(args, "name")
		title := getString(args, "title")
		language := getString(args, "language")
		if language == "" {
			language = getString(args, "lang")
		}
		contentFile := getString(args, "content_file")
		htmlTeaser := getString(args, "html_teaser")

		if language == "" {
			if strings.HasSuffix(contentFile, "_en.html") || strings.HasSuffix(name, "_en") {
				language = "en"
			} else {
				language = "de"
			}
		}

		content := htmlTeaser
		if contentFile != "" {
			fPath := filepath.Join(s.cfg.Server.WebDir, "content", filepath.Clean(contentFile))
			if b, err := os.ReadFile(fPath); err == nil && len(b) > 0 {
				content = string(b)
			}
		}

		systemPrompt := fmt.Sprintf(`You are an expert semantic search and knowledge-indexing AI for Björn Eberhardt's personal website and portfolio (leben.8bj.de).

Your task is to analyze the provided card title, name, and article content, then generate high-quality metadata consisting of:
1. "summary": A dense semantic summary specifically engineered for vector search embeddings (vector similarity retrieval).
2. "tags": 3 to 6 thematic category tags.

STRICT GUIDELINES FOR "summary":
- Audience: Strictly NOT user-facing. It is fed into an embedding model to match user search queries.
- Ground Truth: Derives 100%% from the actual text provided. Faithfully capture all key entities, domain concepts, arguments, technical details, keywords, and practical context.
- High Information Density: Dense, fact-rich prose (~250 to 380 words, matching ~400-500 embedding tokens).
- ZERO Meta-Talk / Filler: NEVER write phrases like "In this article...", "This card describes...", "The author talks about...", "Here we see...", or "Der Text behandelt...". State the facts, relationships, concepts, and key topics directly.
- Language & Tone: Write the summary in the target document language (%s). If German, always follow modern German UX rules with lowercase pronouns ("du", "dir", "dein"), authentic, direct, on eye-level, no bureaucratic or formal letter phrasing.

STRICT GUIDELINES FOR "tags":
- 3 to 6 lowercase thematic keywords representing the core topics, skills, or domains (e.g. ["finanzen", "altersvorsorge", "etf", "steuern"] or ["nixos", "linux", "devops", "cloud"]).

Respond ONLY with a valid, raw JSON object (no markdown code blocks, no backticks, no explanatory text):
{
  "summary": "Direct, fact-dense embedding prose...",
  "tags": ["tag1", "tag2", "tag3"]
}`, language)

		userContent := fmt.Sprintf("Target Language: %s\nCard Name: %s\nTitle: %s\nContent:\n%s", language, name, title, content)
		if s.llmSvc == nil {
			return nil, fmt.Errorf("LLM service unavailable")
		}
		llmRes, err := s.llmSvc.CallLLM(ctx, systemPrompt, userContent)
		if err != nil {
			return nil, err
		}

		cleanJSON := strings.TrimSpace(llmRes)
		cleanJSON = strings.TrimPrefix(cleanJSON, "```json")
		cleanJSON = strings.TrimPrefix(cleanJSON, "```")
		cleanJSON = strings.TrimSuffix(cleanJSON, "```")

		var parsed map[string]any
		if err := json.Unmarshal([]byte(cleanJSON), &parsed); err != nil {
			return nil, fmt.Errorf("LLM returned invalid JSON: %s", cleanJSON)
		}
		return map[string]any{"status": "success", "data": parsed}, nil

	case "edit_html_teaser":
		prompt := getString(args, "prompt")
		htmlTeaser := getString(args, "html_teaser")

		systemPrompt := "You are an expert HTML editor assistant. Modify the HTML document according to the user instruction. Respond ONLY with the modified HTML document string. Do not include markdown block formatting."
		userContent := fmt.Sprintf("Instruction: %s\n\nHTML Document:\n%s", prompt, htmlTeaser)

		if s.llmSvc == nil {
			return nil, fmt.Errorf("LLM service unavailable")
		}
		llmRes, err := s.llmSvc.CallLLM(ctx, systemPrompt, userContent)
		if err != nil {
			return nil, err
		}

		cleanHTML := strings.TrimSpace(llmRes)
		cleanHTML = strings.TrimPrefix(cleanHTML, "```html")
		cleanHTML = strings.TrimPrefix(cleanHTML, "```")
		cleanHTML = strings.TrimSuffix(cleanHTML, "```")

		return map[string]any{"status": "success", "html": cleanHTML}, nil

	case "get_frontend_config":
		data, err := os.ReadFile(filepath.Join(s.cfg.Server.WebDir, "config.json"))
		if err != nil {
			return nil, fmt.Errorf("config.json not found: %w", err)
		}
		var js json.RawMessage
		if err := json.Unmarshal(data, &js); err != nil {
			return nil, fmt.Errorf("invalid config.json format: %w", err)
		}
		return map[string]any{"status": "success", "config": js}, nil

	case "save_frontend_config":
		cfgJSON := getString(args, "config_json")
		if cfgJSON == "" {
			return nil, fmt.Errorf("config_json required")
		}
		var js json.RawMessage
		if err := json.Unmarshal([]byte(cfgJSON), &js); err != nil {
			return nil, fmt.Errorf("invalid JSON: %w", err)
		}
		fPath := filepath.Join(s.cfg.Server.WebDir, "config.json")
		if err := os.WriteFile(fPath, []byte(cfgJSON), 0644); err != nil {
			return nil, err
		}
		return map[string]any{"status": "success", "message": "Configuration saved."}, nil

	case "manage_media":
		kind := getString(args, "kind")
		action := getString(args, "action")
		dirName := "tileimg"
		if kind == "asset" {
			dirName = "assets"
		}
		dirPath := filepath.Join(s.cfg.Server.WebDir, dirName)
		_ = os.MkdirAll(dirPath, 0755)

		switch action {
		case "list":
			entries, err := os.ReadDir(dirPath)
			if err != nil {
				return nil, err
			}
			var files []map[string]any
			for _, e := range entries {
				if e.IsDir() || strings.HasPrefix(e.Name(), ".") {
					continue
				}
				info, err := e.Info()
				if err != nil {
					continue
				}
				files = append(files, map[string]any{
					"name":  e.Name(),
					"size":  info.Size(),
					"mtime": info.ModTime().Unix(),
				})
			}
			return map[string]any{"status": "success", "kind": kind, "files": files}, nil

		case "delete":
			name := filepath.Base(getString(args, "name"))
			if name == "" || name == "." {
				return nil, fmt.Errorf("delete requires name")
			}
			target := filepath.Join(dirPath, name)
			if err := os.Remove(target); err != nil {
				return nil, err
			}
			return map[string]any{"status": "success", "message": fmt.Sprintf("Deleted %s", name)}, nil

		case "rename":
			oldName := filepath.Base(getString(args, "name"))
			newName := filepath.Base(getString(args, "new_name"))
			if oldName == "" || newName == "" {
				return nil, fmt.Errorf("rename requires name and new_name")
			}
			src := filepath.Join(dirPath, oldName)
			dst := filepath.Join(dirPath, newName)
			if err := os.Rename(src, dst); err != nil {
				return nil, err
			}
			return map[string]any{"status": "success", "message": fmt.Sprintf("Renamed %s to %s", oldName, newName)}, nil

		case "upload":
			name := filepath.Base(getString(args, "name"))
			dataStr := getString(args, "data")
			filePath := getString(args, "path")
			var fileBytes []byte
			var err error

			if dataStr != "" {
				if b, decErr := base64.StdEncoding.DecodeString(dataStr); decErr == nil {
					fileBytes = b
				} else {
					fileBytes = []byte(dataStr)
				}
			} else if filePath != "" {
				fileBytes, err = os.ReadFile(filePath)
				if err != nil {
					return nil, fmt.Errorf("cannot read local file '%s': %w", filePath, err)
				}
				if name == "" || name == "." {
					name = filepath.Base(filePath)
				}
			} else {
				return nil, fmt.Errorf("upload requires either path or data")
			}

			if name == "" || name == "." {
				name = "uploaded_media.bin"
			}

			target := filepath.Join(dirPath, name)
			if err := os.WriteFile(target, fileBytes, 0644); err != nil {
				return nil, err
			}
			return map[string]any{"status": "success", "message": fmt.Sprintf("Uploaded %s (%d bytes)", name, len(fileBytes)), "name": name}, nil

		default:
			return nil, fmt.Errorf("unknown action '%s' for manage_media", action)
		}

	case "manage_api_tokens":
		action := getString(args, "action")
		if action == "" || action == "list" {
			if s.auth == nil {
				return nil, fmt.Errorf("auth service unavailable")
			}
			tokens, err := s.auth.ListAPITokens()
			if err != nil {
				return nil, err
			}
			return map[string]any{"status": "success", "tokens": tokens}, nil
		} else if action == "revoke" {
			id := getInt(args, "id")
			if id <= 0 {
				return nil, fmt.Errorf("revoke requires numeric token id")
			}
			if s.auth == nil {
				return nil, fmt.Errorf("auth service unavailable")
			}
			if err := s.auth.RevokeAPIToken(id); err != nil {
				return nil, err
			}
			return map[string]any{"status": "success", "message": fmt.Sprintf("Token %d revoked.", id)}, nil
		}
		return nil, fmt.Errorf("unknown action '%s' for manage_api_tokens", action)

	default:
		return nil, fmt.Errorf("unknown tool '%s'", name)
	}
}

// Helpers for safe argument extraction from map[string]any
func getInt(args map[string]any, key string) int {
	if val, ok := args[key]; ok {
		switch v := val.(type) {
		case float64:
			return int(v)
		case int:
			return v
		case int64:
			return int(v)
		}
	}
	return 0
}

func getString(args map[string]any, key string) string {
	if val, ok := args[key]; ok {
		if s, ok := val.(string); ok {
			return strings.TrimSpace(s)
		}
	}
	return ""
}

func getBool(args map[string]any, key string, defaultVal bool) bool {
	if val, ok := args[key]; ok {
		if b, ok := val.(bool); ok {
			return b
		}
	}
	return defaultVal
}
