package router

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/julienschmidt/httprouter"
	"github.com/windowsfreak/leben/internal/auth"
	"github.com/windowsfreak/leben/internal/config"
	"github.com/windowsfreak/leben/internal/mcp"
	"github.com/windowsfreak/leben/internal/models"
	"github.com/windowsfreak/leben/internal/services"
	"github.com/windowsfreak/leben/internal/tasks"
	"github.com/windowsfreak/leben/internal/toon"
)

type Router struct {
	cfg       *config.Config
	auth      *auth.Auth
	tileSvc   *services.TileService
	transSvc  *services.TranslationService
	llmSvc    *services.LLMService
	taskMgr   *tasks.Manager
	mcpServer *mcp.Server
	router    *httprouter.Router
}

func New(
	cfg *config.Config,
	auth *auth.Auth,
	tileSvc *services.TileService,
	transSvc *services.TranslationService,
	llmSvc *services.LLMService,
	taskMgr *tasks.Manager,
	mcpServer *mcp.Server,
) *Router {
	r := &Router{
		cfg:       cfg,
		auth:      auth,
		tileSvc:   tileSvc,
		transSvc:  transSvc,
		llmSvc:    llmSvc,
		taskMgr:   taskMgr,
		mcpServer: mcpServer,
		router:    httprouter.New(),
	}

	r.setupRoutes()
	return r
}

func (r *Router) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	// CORS headers
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Admin-Token, X-Admin-Password, X-Reference")

	if req.Method == "OPTIONS" {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	r.router.ServeHTTP(w, req)
}

func (r *Router) setupRoutes() {
	// Public Endpoints
	r.router.GET("/api/tiles", r.handleGetTiles)
	r.router.GET("/api/tiles/:name", r.handleGetTileByName)
	r.router.GET("/api/tiles/:name/versions", r.handleGetTileVersions)

	// MCP Endpoints
	r.router.GET("/mcp", r.handleMCPGet)
	r.router.POST("/mcp", r.handleMCPPost)

	// Admin Public Login
	r.router.POST("/api/admin/login", r.handleAdminLogin)

	// Admin Protected Endpoints
	r.router.GET("/api/admin/tiles", r.wrapAuth(r.handleAdminGetAllTiles))
	r.router.POST("/api/admin/tiles", r.wrapAuth(r.handleAdminSaveTile))
	r.router.PUT("/api/admin/tiles", r.wrapAuth(r.handleAdminSaveTile))
	r.router.POST("/api/admin/tiles/refresh-vectors", r.wrapAuth(r.handleAdminRefreshVectors))

	r.router.DELETE("/api/admin/tile/:id", r.wrapAuth(r.handleAdminDeleteTile))
	r.router.DELETE("/api/admin/tiles/:id", r.wrapAuth(r.handleAdminDeleteTile))
	r.router.POST("/api/admin/tile/:id/toggle-visibility", r.wrapAuth(r.handleAdminToggleVisibility))
	r.router.POST("/api/admin/tile/:id/clone", r.wrapAuth(r.handleAdminCloneTile))
	r.router.POST("/api/admin/tile/:id/translate", r.wrapAuth(r.handleAdminTranslateTile))
	r.router.GET("/api/admin/translation-status", r.wrapAuth(r.handleAdminGetTranslationStatus))
	r.router.POST("/api/admin/config", r.wrapAuth(r.handleAdminSaveConfig))

	r.router.GET("/api/admin/tasks", r.wrapAuth(r.handleAdminGetTasks))
	r.router.POST("/api/admin/tasks/:id/cancel", r.wrapAuth(r.handleAdminCancelTask))

	r.router.GET("/api/admin/content/:file", r.wrapAuth(r.handleAdminGetContentFile))
	r.router.POST("/api/admin/content/:file", r.wrapAuth(r.handleAdminSaveContentFile))
	r.router.POST("/api/admin/content-suggest-meta", r.wrapAuth(r.handleAdminSuggestMeta))
	r.router.POST("/api/admin/content-edit-html", r.wrapAuth(r.handleAdminEditHTMLWithLLM))

	r.router.GET("/api/admin/images", r.wrapAuth(r.handleAdminListImages))
	r.router.POST("/api/admin/images/upload", r.wrapAuth(r.handleAdminUploadImage))
	r.router.POST("/api/admin/images/rename", r.wrapAuth(r.handleAdminRenameImage))
	r.router.DELETE("/api/admin/images/:name", r.wrapAuth(r.handleAdminDeleteImage))

	r.router.GET("/api/admin/assets", r.wrapAuth(r.handleAdminListAssets))
	r.router.POST("/api/admin/assets/upload", r.wrapAuth(r.handleAdminUploadAsset))
	r.router.POST("/api/admin/assets/rename", r.wrapAuth(r.handleAdminRenameAsset))
	r.router.DELETE("/api/admin/assets/:name", r.wrapAuth(r.handleAdminDeleteAsset))

	// Static file serving fallback for local dev
	r.router.NotFound = http.FileServer(http.Dir(r.cfg.Server.WebDir))
}

func (r *Router) wrapAuth(handler http.HandlerFunc) httprouter.Handle {
	return func(w http.ResponseWriter, req *http.Request, ps httprouter.Params) {
		ctx := context.WithValue(req.Context(), httprouter.ParamsKey, ps)
		r.auth.Middleware(handler)(w, req.WithContext(ctx))
	}
}

// Handler Implementations

func (r *Router) handleGetTiles(w http.ResponseWriter, req *http.Request, _ httprouter.Params) {
	qParams := req.URL.Query()
	qStr := strings.TrimSpace(qParams.Get("q"))
	similarName := strings.TrimSpace(qParams.Get("similar"))
	lang := strings.ToLower(strings.TrimSpace(qParams.Get("lang")))
	if lang == "" {
		lang = "de"
	}

	format := strings.ToLower(strings.TrimSpace(qParams.Get("format")))
	acceptHeader := req.Header.Get("Accept")
	if format == "" {
		if strings.Contains(acceptHeader, "text/x-toon") || strings.Contains(acceptHeader, "toon") {
			format = "toon"
		} else {
			format = "json"
		}
	}

	detail := strings.ToLower(strings.TrimSpace(qParams.Get("detail")))
	if detail == "" {
		detail = "snippet"
	}

	crop, _ := strconv.Atoi(qParams.Get("crop"))
	offset, _ := strconv.Atoi(qParams.Get("offset"))

	// Explicit limit handling: default 20 if limit parameter omitted; if limit=0, return all
	limit := 20
	if qParams.Has("limit") {
		limitVal, err := strconv.Atoi(qParams.Get("limit"))
		if err == nil {
			limit = limitVal
		}
	}

	showInvisible := r.auth.IsAdmin(req)
	refCodes := getRequestReferenceCodes(req)

	var tiles []*models.Tile
	var err error

	if similarName != "" {
		tiles, err = r.tileSvc.GetSimilarTiles(req.Context(), similarName, lang, refCodes, showInvisible, limit, offset)
	} else {
		tiles, err = r.tileSvc.SearchTiles(req.Context(), lang, qStr, refCodes, showInvisible, offset, limit)
	}

	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	contentsDir := filepath.Join(r.cfg.Server.WebDir, "content")

	var items []toon.Item
	for idx, t := range tiles {
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

		scoreStr := ""
		if (qStr != "" || similarName != "") && t.Distance > 0 && t.Distance <= 2.0 {
			sim := 1.0 - t.Distance
			if sim < 0 {
				sim = 0
			}
			if sim > 1 {
				sim = 1
			}
			scoreStr = fmt.Sprintf("%.2f", sim)
		}

		summaryText := t.Summary
		bodyText := ""

		if detail == "full" {
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
		}

		if detail == "snippet" && crop <= 0 {
			crop = 70
		}
		if crop > 0 {
			if len(summaryText) > crop {
				summaryText = summaryText[:crop] + "..."
			}
			if len(bodyText) > crop {
				bodyText = bodyText[:crop] + "..."
			}
		}

		items = append(items, toon.Item{
			Index:   offset + idx + 1,
			Name:    t.Name,
			Title:   t.Title,
			Lang:    t.Language,
			Tags:    tagsStr,
			Type:    typeStr,
			Date:    dateStr,
			Score:   scoreStr,
			Summary: summaryText,
			Content: bodyText,
			Link:    t.Link,
		})
	}

	if format == "toon" {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("X-LLM-Format", "TOON")
		output, err := toon.FormatTOON(qStr, lang, detail, items)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		w.Write([]byte(output))
		return
	}

	// Default JSON format
	writeJSON(w, http.StatusOK, map[string]any{
		"status": "success",
		"query":  qStr,
		"lang":   lang,
		"format": detail,
		"count":  len(tiles),
		"data":   tiles,
		"items":  items,
	})
}

func (r *Router) handleGetTileByName(w http.ResponseWriter, req *http.Request, ps httprouter.Params) {
	name := ps.ByName("name")
	lang := strings.ToLower(strings.TrimSpace(req.URL.Query().Get("lang")))
	if lang == "" {
		lang = "de"
	}
	showInvisible := r.auth.IsAdmin(req)
	refCodes := getRequestReferenceCodes(req)

	tile, err := r.tileSvc.GetTile(req.Context(), name, lang, refCodes, showInvisible)
	if err != nil {
		writeError(w, http.StatusNotFound, fmt.Sprintf("Tile '%s' not found: %v", name, err))
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"status": "success",
		"data":   tile,
	})
}

func (r *Router) handleGetTileVersions(w http.ResponseWriter, req *http.Request, ps httprouter.Params) {
	name := ps.ByName("name")
	showInvisible := r.auth.IsAdmin(req)
	refCodes := getRequestReferenceCodes(req)

	versions, err := r.tileSvc.GetTileInfo(req.Context(), name, refCodes, showInvisible)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"status":   "success",
		"name":     name,
		"versions": versions,
	})
}

func (r *Router) handleMCPGet(w http.ResponseWriter, req *http.Request, _ httprouter.Params) {
	if req.URL.Query().Get("action") == "sse" || req.Header.Get("Accept") == "text/event-stream" {
		r.mcpServer.HandleSSE(w, req)
		return
	}
	r.mcpServer.HandleMessage(w, req)
}

func (r *Router) handleMCPPost(w http.ResponseWriter, req *http.Request, _ httprouter.Params) {
	r.mcpServer.HandleMessage(w, req)
}

func (r *Router) handleAdminLogin(w http.ResponseWriter, req *http.Request, _ httprouter.Params) {
	var body struct {
		Password string `json:"password"`
	}
	if strings.Contains(req.Header.Get("Content-Type"), "application/json") {
		_ = json.NewDecoder(req.Body).Decode(&body)
	}
	password := body.Password
	if password == "" {
		password = req.URL.Query().Get("password")
	}

	if !r.auth.VerifyPassword(password) {
		writeError(w, http.StatusUnauthorized, "Invalid admin password.")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"status":  "success",
		"message": "Login successful.",
		"token":   r.cfg.Admin.SecretToken,
	})
}

func (r *Router) handleAdminGetAllTiles(w http.ResponseWriter, req *http.Request) {
	tiles, err := r.tileSvc.GetAllTiles(req.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"status": "success",
		"tiles":  tiles,
	})
}

func (r *Router) handleAdminSaveTile(w http.ResponseWriter, req *http.Request) {
	var tile models.Tile
	if err := json.NewDecoder(req.Body).Decode(&tile); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid JSON body.")
		return
	}

	if tile.Name == "" || tile.Title == "" {
		writeError(w, http.StatusBadRequest, "Name and Title are required.")
		return
	}

	if err := r.tileSvc.SaveTile(req.Context(), &tile); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"status":  "success",
		"message": "Tile saved successfully.",
		"tile":    tile,
	})
}

func (r *Router) handleAdminDeleteTile(w http.ResponseWriter, req *http.Request) {
	ps := httprouter.ParamsFromContext(req.Context())
	id, err := strconv.Atoi(ps.ByName("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "Invalid tile ID.")
		return
	}
	if err := r.tileSvc.DeleteTile(req.Context(), id); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "success", "message": "Tile deleted."})
}

func (r *Router) handleAdminTranslateTile(w http.ResponseWriter, req *http.Request) {
	ps := httprouter.ParamsFromContext(req.Context())
	name := ps.ByName("id")
	if name == "" {
		name = ps.ByName("name")
	}

	var body struct {
		Name       string `json:"name"`
		TargetLang string `json:"target_lang"`
	}
	_ = json.NewDecoder(req.Body).Decode(&body)

	if name == "" {
		name = body.Name
	}
	targetLang := body.TargetLang
	if targetLang == "" {
		targetLang = req.URL.Query().Get("target_lang")
	}
	if targetLang == "" {
		targetLang = "all"
	}

	task, err := r.transSvc.StartAutoTranslateTask(req.Context(), name, targetLang)
	if err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"status":  "success",
		"message": fmt.Sprintf("Translation task started for card '%s'.", name),
		"task":    task,
	})
}

func (r *Router) handleAdminGetTasks(w http.ResponseWriter, req *http.Request) {
	tasksList := r.taskMgr.GetTasks()
	writeJSON(w, http.StatusOK, map[string]any{
		"status": "success",
		"tasks":  tasksList,
	})
}

func (r *Router) handleAdminCancelTask(w http.ResponseWriter, req *http.Request) {
	ps := httprouter.ParamsFromContext(req.Context())
	id := ps.ByName("id")

	if r.taskMgr.CancelTask(id) {
		writeJSON(w, http.StatusOK, map[string]any{
			"status":  "success",
			"message": fmt.Sprintf("Task '%s' cancelled successfully.", id),
		})
		return
	}
	writeError(w, http.StatusBadRequest, fmt.Sprintf("Task '%s' is not running or not found.", id))
}

func (r *Router) handleAdminGetContentFile(w http.ResponseWriter, req *http.Request) {
	ps := httprouter.ParamsFromContext(req.Context())
	file := ps.ByName("file")
	if file == "" {
		file = filepath.Base(req.URL.Path)
	}
	fPath := filepath.Join(r.cfg.Server.WebDir, "content", file)

	info, err := os.Stat(fPath)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"status":  "success",
			"content": "",
			"mtime":   0,
		})
		return
	}

	bytes, err := os.ReadFile(fPath)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"status":  "success",
		"content": string(bytes),
		"mtime":   info.ModTime().Unix(),
	})
}

func (r *Router) handleAdminSaveContentFile(w http.ResponseWriter, req *http.Request) {
	ps := httprouter.ParamsFromContext(req.Context())
	file := ps.ByName("file")
	if file == "" {
		file = filepath.Base(req.URL.Path)
	}

	var body struct {
		Content       string `json:"content"`
		ExpectedMTime int64  `json:"expected_mtime"`
	}
	_ = json.NewDecoder(req.Body).Decode(&body)

	fPath := filepath.Join(r.cfg.Server.WebDir, "content", file)

	if info, err := os.Stat(fPath); err == nil && body.ExpectedMTime > 0 {
		actualMTime := info.ModTime().Unix()
		if actualMTime > body.ExpectedMTime {
			writeJSON(w, http.StatusConflict, map[string]any{
				"status":       "error",
				"message":      "Conflict: Content file has been modified on the server.",
				"actual_mtime": actualMTime,
			})
			return
		}
	}

	if err := os.WriteFile(fPath, []byte(body.Content), 0644); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	info, _ := os.Stat(fPath)
	mtime := int64(0)
	if info != nil {
		mtime = info.ModTime().Unix()
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"status":  "success",
		"message": "File saved successfully.",
		"mtime":   mtime,
	})
}

func (r *Router) handleAdminSuggestMeta(w http.ResponseWriter, req *http.Request) {
	var body struct {
		Name        string `json:"name"`
		Title       string `json:"title"`
		ContentFile string `json:"content_file"`
		HTMLTeaser  string `json:"html_teaser"`
	}
	_ = json.NewDecoder(req.Body).Decode(&body)

	content := body.HTMLTeaser
	if body.ContentFile != "" {
		fPath := filepath.Join(r.cfg.Server.WebDir, "content", body.ContentFile)
		if bytes, err := os.ReadFile(fPath); err == nil {
			content = string(bytes)
		}
	}

	systemPrompt := `You are an AI assistant helping organize content tiles. Analyze the content and generate:
1. A concise, high-level summary (up to 400 words).
2. 3 to 6 category tags representing main themes.

Respond ONLY with a valid JSON object matching this structure (no markdown formatting, no backticks):
{
  "summary": "Summary text...",
  "tags": ["tag1", "tag2"]
}`

	userContent := fmt.Sprintf("Name: %s\nTitle: %s\nContent:\n%s", body.Name, body.Title, content)
	llmRes, err := r.llmSvc.CallLLM(req.Context(), systemPrompt, userContent)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	cleanJSON := strings.TrimSpace(llmRes)
	cleanJSON = strings.TrimPrefix(cleanJSON, "```json")
	cleanJSON = strings.TrimPrefix(cleanJSON, "```")
	cleanJSON = strings.TrimSuffix(cleanJSON, "```")

	var parsed map[string]any
	if err := json.Unmarshal([]byte(cleanJSON), &parsed); err != nil {
		writeError(w, http.StatusInternalServerError, "LLM returned invalid JSON: "+cleanJSON)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"status": "success",
		"data":   parsed,
	})
}

func (r *Router) handleAdminEditHTMLWithLLM(w http.ResponseWriter, req *http.Request) {
	var body struct {
		Prompt     string `json:"prompt"`
		HTMLTeaser string `json:"html_teaser"`
	}
	_ = json.NewDecoder(req.Body).Decode(&body)

	systemPrompt := "You are an expert HTML editor assistant. Modify the HTML document according to the user instruction. Respond ONLY with the modified HTML document string. Do not include markdown block formatting."
	userContent := fmt.Sprintf("Instruction: %s\n\nHTML Document:\n%s", body.Prompt, body.HTMLTeaser)

	llmRes, err := r.llmSvc.CallLLM(req.Context(), systemPrompt, userContent)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	cleanHTML := strings.TrimSpace(llmRes)
	cleanHTML = strings.TrimPrefix(cleanHTML, "```html")
	cleanHTML = strings.TrimPrefix(cleanHTML, "```")
	cleanHTML = strings.TrimSuffix(cleanHTML, "```")

	writeJSON(w, http.StatusOK, map[string]any{
		"status":      "success",
		"html_teaser": cleanHTML,
	})
}

// Image & Asset Handlers

func (r *Router) handleAdminListImages(w http.ResponseWriter, req *http.Request) {
	r.listDirFiles(w, "tileimg")
}

func (r *Router) handleAdminUploadImage(w http.ResponseWriter, req *http.Request) {
	r.uploadFile(w, req, "tileimg")
}

func (r *Router) handleAdminRenameImage(w http.ResponseWriter, req *http.Request) {
	r.renameFile(w, req, "tileimg")
}

func (r *Router) handleAdminDeleteImage(w http.ResponseWriter, req *http.Request) {
	ps := httprouter.ParamsFromContext(req.Context())
	r.deleteFile(w, "tileimg", ps.ByName("name"))
}

func (r *Router) handleAdminListAssets(w http.ResponseWriter, req *http.Request) {
	r.listDirFiles(w, "assets")
}

func (r *Router) handleAdminUploadAsset(w http.ResponseWriter, req *http.Request) {
	r.uploadFile(w, req, "assets")
}

func (r *Router) handleAdminRenameAsset(w http.ResponseWriter, req *http.Request) {
	r.renameFile(w, req, "assets")
}

func (r *Router) handleAdminDeleteAsset(w http.ResponseWriter, req *http.Request) {
	ps := httprouter.ParamsFromContext(req.Context())
	r.deleteFile(w, "assets", ps.ByName("name"))
}

// File system helpers

func (r *Router) listDirFiles(w http.ResponseWriter, dirName string) {
	dirPath := filepath.Join(r.cfg.Server.WebDir, dirName)
	entries, err := os.ReadDir(dirPath)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	var files []map[string]any
	for _, e := range entries {
		if !e.IsDir() && !strings.HasPrefix(e.Name(), ".") {
			info, _ := e.Info()
			files = append(files, map[string]any{
				"name":  e.Name(),
				"url":   fmt.Sprintf("./%s/%s", dirName, e.Name()),
				"size":  info.Size(),
				"mtime": info.ModTime().Unix(),
			})
		}
	}

	res := map[string]any{
		"status": "success",
		"files":  files,
	}
	if dirName == "tileimg" || dirName == "img" {
		res["images"] = files
	} else if dirName == "assets" {
		res["assets"] = files
	}

	writeJSON(w, http.StatusOK, res)
}

func (r *Router) uploadFile(w http.ResponseWriter, req *http.Request, dirName string) {
	file, header, err := req.FormFile("file")
	if err != nil {
		file, header, err = req.FormFile("image")
	}
	if err != nil {
		writeError(w, http.StatusBadRequest, "No file uploaded.")
		return
	}
	defer file.Close()

	destPath := filepath.Join(r.cfg.Server.WebDir, dirName, filepath.Base(header.Filename))
	outFile, err := os.Create(destPath)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer outFile.Close()

	if _, err := io.Copy(outFile, file); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	fileInfo := map[string]any{
		"name": header.Filename,
		"url":  fmt.Sprintf("./%s/%s", dirName, header.Filename),
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"status":  "success",
		"message": "File uploaded successfully.",
		"name":    header.Filename,
		"image":   fileInfo,
		"file":    fileInfo,
	})
}

func (r *Router) renameFile(w http.ResponseWriter, req *http.Request, dirName string) {
	var body struct {
		OldName string `json:"old_name"`
		NewName string `json:"new_name"`
	}
	_ = json.NewDecoder(req.Body).Decode(&body)

	oldPath := filepath.Join(r.cfg.Server.WebDir, dirName, filepath.Base(body.OldName))
	newPath := filepath.Join(r.cfg.Server.WebDir, dirName, filepath.Base(body.NewName))

	if err := os.Rename(oldPath, newPath); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"status":  "success",
		"message": "File renamed successfully.",
	})
}

func (r *Router) deleteFile(w http.ResponseWriter, dirName, name string) {
	filePath := filepath.Join(r.cfg.Server.WebDir, dirName, filepath.Base(name))
	if err := os.Remove(filePath); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"status":  "success",
		"message": "File deleted.",
	})
}

func getRequestReferenceCodes(r *http.Request) []string {
	refHeader := r.Header.Get("X-Reference")
	if refHeader == "" {
		return nil
	}
	parts := strings.Split(refHeader, ",")
	var cleaned []string
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			cleaned = append(cleaned, p)
		}
	}
	return cleaned
}

func writeJSON(w http.ResponseWriter, code int, data any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(data)
}

func writeError(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]any{
		"status":  "error",
		"message": msg,
	})
}

func (r *Router) handleAdminSaveConfig(w http.ResponseWriter, req *http.Request) {
	var body struct {
		ConfigJSON string `json:"config_json"`
	}
	_ = json.NewDecoder(req.Body).Decode(&body)

	if body.ConfigJSON == "" {
		writeError(w, http.StatusBadRequest, "No config_json provided.")
		return
	}

	var js json.RawMessage
	if err := json.Unmarshal([]byte(body.ConfigJSON), &js); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid JSON structure.")
		return
	}

	destPath := filepath.Join(r.cfg.Server.WebDir, "config.json")
	if err := os.WriteFile(destPath, []byte(body.ConfigJSON), 0644); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"status":  "success",
		"message": "Configuration saved successfully.",
	})
}

func (r *Router) handleAdminGetTranslationStatus(w http.ResponseWriter, req *http.Request) {
	name := req.URL.Query().Get("name")
	matrix, err := r.tileSvc.GetTranslationStatus(req.Context(), name)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if name != "" {
		if tileData, ok := matrix[name]; ok {
			writeJSON(w, http.StatusOK, map[string]any{
				"status": "success",
				"data":   tileData,
			})
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"status": "success",
		"data":   matrix,
	})
}

func (r *Router) handleAdminRefreshVectors(w http.ResponseWriter, req *http.Request) {
	count, err := r.tileSvc.RefreshVectors(req.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"status":  "success",
		"message": fmt.Sprintf("Vectors refreshed successfully for %d tiles.", count),
	})
}

func (r *Router) handleAdminToggleVisibility(w http.ResponseWriter, req *http.Request) {
	ps := httprouter.ParamsFromContext(req.Context())
	id, err := strconv.Atoi(ps.ByName("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "Invalid tile ID.")
		return
	}
	if err := r.tileSvc.ToggleVisibility(req.Context(), id); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "success", "message": "Tile visibility toggled."})
}

func (r *Router) handleAdminCloneTile(w http.ResponseWriter, req *http.Request) {
	ps := httprouter.ParamsFromContext(req.Context())
	id, err := strconv.Atoi(ps.ByName("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "Invalid tile ID.")
		return
	}
	clone, err := r.tileSvc.CloneTile(req.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"status":     "success",
		"message":    "Tile cloned successfully.",
		"clone_name": clone.Name,
	})
}

