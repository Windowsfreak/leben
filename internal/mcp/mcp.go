package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/windowsfreak/leben/internal/config"
	"github.com/windowsfreak/leben/internal/models"
	"github.com/windowsfreak/leben/internal/services"
	"github.com/windowsfreak/leben/internal/tasks"
)

type Server struct {
	cfg       *config.Config
	tileSvc   *services.TileService
	transSvc  *services.TranslationService
	taskMgr   *tasks.Manager
	sessions  sync.Map // sid -> chan string
}

func NewServer(
	cfg *config.Config,
	tileSvc *services.TileService,
	transSvc *services.TranslationService,
	taskMgr *tasks.Manager,
) *Server {
	return &Server{
		cfg:      cfg,
		tileSvc:  tileSvc,
		transSvc: transSvc,
		taskMgr:  taskMgr,
	}
}

func (s *Server) GetTools() []models.MCPTool {
	langs := []string{"de", "en"}
	targetLangs := []string{"de", "en", "all"}

	return []models.MCPTool{
		{
			Name:        "search_tiles",
			Description: "Semantic vector search across profile cards by query or returns ranking. Returns tile metadata, teasers, and similarity scores.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"q":      map[string]any{"type": "string", "description": "Natural language query or keywords"},
					"lang":   map[string]any{"type": "string", "enum": langs, "description": "Preferred language (default 'de')"},
					"limit":  map[string]any{"type": "integer", "description": "Maximum tiles to return (default 20)"},
					"offset": map[string]any{"type": "integer", "description": "Pagination offset (default 0)"},
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
					"name":            map[string]any{"type": "string", "description": "Name of the card"},
					"lang":            map[string]any{"type": "string", "enum": langs, "description": "Language code (default 'de')"},
					"include_content": map[string]any{"type": "boolean", "description": "Whether to include full HTML content from file"},
				},
				"required": []string{"name"},
			},
		},
		{
			Name:        "translate_tile",
			Description: "Trigger an independent asynchronous translation task to translate tile metadata and HTML content.",
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
			Name:        "get_task_status",
			Description: "Get the status of running or completed background translation tasks.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"id": map[string]any{"type": "string", "description": "Task ID (optional, returns all if omitted)"},
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
			Name:        "save_content_file",
			Description: "Save or update HTML content file in the /content directory.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"file":    map[string]any{"type": "string", "description": "Filename (e.g. 'contact_de.html')"},
					"content": map[string]any{"type": "string", "description": "Full HTML content"},
				},
				"required": []string{"file", "content"},
			},
		},
	}
}

// HandleSSE manages SSE (Server-Sent Events) streaming connection
func (s *Server) HandleSSE(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming unsupported", http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	sessionID := uuid.New().String()
	msgChan := make(chan string, 10)
	s.sessions.Store(sessionID, msgChan)
	defer s.sessions.Delete(sessionID)

	scheme := "http"
	if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
		scheme = "https"
	}
	messageURL := fmt.Sprintf("%s://%s/mcp?action=message&sessionId=%s", scheme, r.Host, sessionID)

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

// HandleMessage manages HTTP POST JSON-RPC 2.0 calls
func (s *Server) HandleMessage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	var req models.MCPRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(models.MCPResponse{
			JSONRPC: "2.0",
			Error:   map[string]any{"code": -32700, "message": "Parse error"},
		})
		return
	}

	resp := s.ExecuteMethod(r.Context(), req)

	// Send to session SSE queue if session specified
	sid := r.URL.Query().Get("sessionId")
	if sid != "" {
		if chVal, ok := s.sessions.Load(sid); ok {
			msgChan := chVal.(chan string)
			b, _ := json.Marshal(resp)
			select {
			case msgChan <- string(b):
			default:
			}
		}
	}

	_ = json.NewEncoder(w).Encode(resp)
}

func (s *Server) ExecuteMethod(ctx context.Context, req models.MCPRequest) models.MCPResponse {
	switch req.Method {
	case "initialize":
		return models.MCPResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result: map[string]any{
				"protocolVersion": "2024-11-05",
				"capabilities":    map[string]any{"tools": map[string]any{}},
				"serverInfo":      map[string]any{"name": "leben-mcp", "version": "1.0.0"},
			},
		}

	case "tools/list":
		return models.MCPResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result: map[string]any{
				"tools": s.GetTools(),
			},
		}

	case "tools/call":
		name, _ := req.Params["name"].(string)
		args, _ := req.Params["arguments"].(map[string]any)

		res, err := s.ExecuteTool(ctx, name, args)
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

func (s *Server) ExecuteTool(ctx context.Context, name string, args map[string]any) (any, error) {
	if args == nil {
		args = make(map[string]any)
	}

	switch name {
	case "search_tiles":
		q, _ := args["q"].(string)
		lang, _ := args["lang"].(string)
		if lang == "" {
			lang = "de"
		}
		tiles, err := s.tileSvc.SearchTiles(ctx, lang, q, nil, false, 0, 20)
		if err != nil {
			return nil, err
		}
		return tiles, nil

	case "get_similar_tiles":
		tName, _ := args["name"].(string)
		lang, _ := args["lang"].(string)
		if lang == "" {
			lang = "de"
		}
		tiles, err := s.tileSvc.GetSimilarTiles(ctx, tName, lang, nil, false, 5, 0)
		if err != nil {
			return nil, err
		}
		return tiles, nil

	case "get_tile":
		tName, _ := args["name"].(string)
		lang, _ := args["lang"].(string)
		if lang == "" {
			lang = "de"
		}
		tile, err := s.tileSvc.GetTile(ctx, tName, lang, nil, false)
		if err != nil {
			return nil, err
		}
		return tile, nil

	case "translate_tile":
		tName, _ := args["name"].(string)
		targetLang, _ := args["target_lang"].(string)
		if targetLang == "" {
			targetLang = "all"
		}
		task, err := s.transSvc.StartAutoTranslateTask(ctx, tName, targetLang)
		if err != nil {
			return nil, err
		}
		return task, nil

	case "get_task_status":
		tID, _ := args["id"].(string)
		if tID != "" {
			if task, ok := s.taskMgr.GetTask(tID); ok {
				return task, nil
			}
			return nil, fmt.Errorf("task '%s' not found", tID)
		}
		return s.taskMgr.GetTasks(), nil

	case "cancel_task":
		tID, _ := args["id"].(string)
		if s.taskMgr.CancelTask(tID) {
			return map[string]string{"status": "success", "message": "Task cancelled successfully"}, nil
		}
		return nil, fmt.Errorf("task '%s' not running or not found", tID)

	case "save_content_file":
		file, _ := args["file"].(string)
		content, _ := args["content"].(string)
		fPath := filepath.Join(s.cfg.Server.WebDir, "content", file)
		if err := os.WriteFile(fPath, []byte(content), 0644); err != nil {
			return nil, err
		}
		return map[string]string{"status": "success", "message": "Content file saved"}, nil

	default:
		return nil, fmt.Errorf("unknown tool '%s'", name)
	}
}
