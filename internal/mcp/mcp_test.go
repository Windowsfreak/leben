package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/windowsfreak/leben/internal/config"
	"github.com/windowsfreak/leben/internal/db"
	"github.com/windowsfreak/leben/internal/models"
)

func TestMCPInitializeAndPing(t *testing.T) {
	s := NewServer(&config.Config{}, nil, nil, nil, nil, nil)

	// initialize
	initReq := models.MCPRequest{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "initialize",
		Params:  map[string]any{"protocolVersion": "2024-11-05"},
	}
	resp := s.ExecuteMethod(context.Background(), initReq, false)
	if resp.Error != nil {
		t.Fatalf("expected nil error, got %v", resp.Error)
	}
	resMap, ok := resp.Result.(map[string]any)
	if !ok {
		t.Fatalf("expected result map, got %T", resp.Result)
	}
	if resMap["protocolVersion"] != "2024-11-05" {
		t.Errorf("expected 2024-11-05, got %v", resMap["protocolVersion"])
	}

	// ping
	pingReq := models.MCPRequest{
		JSONRPC: "2.0",
		ID:      2,
		Method:  "ping",
	}
	resp = s.ExecuteMethod(context.Background(), pingReq, false)
	if resp.Error != nil {
		t.Fatalf("expected nil error on ping, got %v", resp.Error)
	}
}

func TestMCPToolsListPublicVsAdmin(t *testing.T) {
	s := NewServer(&config.Config{}, nil, nil, nil, nil, nil)

	publicTools := s.GetTools(false)
	for _, tool := range publicTools {
		if strings.HasPrefix(tool.Name, "admin_") || tool.Name == "save_content_file" || tool.Name == "translate_tile" {
			t.Errorf("admin tool %s should not be listed publicly", tool.Name)
		}
	}
	if len(publicTools) < 5 {
		t.Errorf("expected at least 5 public tools, got %d", len(publicTools))
	}

	adminTools := s.GetTools(true)
	expectedTools := []string{
		"search_tiles", "get_similar_tiles", "get_tile", "get_tile_versions", "check_auth",
		"admin_list_tiles", "admin_save_tile", "admin_update_tile_fields", "admin_delete_tile",
		"admin_toggle_visibility", "admin_clone_tile", "translate_tile", "translation_status",
		"refresh_vectors", "list_tasks", "cancel_task", "get_content_file",
		"save_content_file", "suggest_meta", "edit_html_teaser", "get_frontend_config",
		"save_frontend_config", "manage_media", "manage_api_tokens",
	}

	toolMap := make(map[string]bool)
	for _, tool := range adminTools {
		toolMap[tool.Name] = true
	}

	for _, name := range expectedTools {
		if !toolMap[name] {
			t.Errorf("missing expected tool in admin tools: %s", name)
		}
	}
	if len(adminTools) < 20 {
		t.Errorf("expected at least 20 tools for admin, got %d", len(adminTools))
	}
}

func TestMCPToolAuthCheck(t *testing.T) {
	s := NewServer(&config.Config{}, nil, nil, nil, nil, nil)

	// check_auth public
	res, err := s.ExecuteTool(context.Background(), "check_auth", nil, false)
	if err != nil {
		t.Fatalf("check_auth public returned error: %v", err)
	}
	resMap := res.(map[string]any)
	if resMap["authenticated"] != false || resMap["role"] != "anonymous" {
		t.Errorf("unexpected check_auth anonymous: %v", resMap)
	}

	// check_auth admin
	res, err = s.ExecuteTool(context.Background(), "check_auth", nil, true)
	if err != nil {
		t.Fatalf("check_auth admin returned error: %v", err)
	}
	resMap = res.(map[string]any)
	if resMap["authenticated"] != true || resMap["role"] != "admin" {
		t.Errorf("unexpected check_auth admin: %v", resMap)
	}

	// Calling admin tool unauthenticated must fail
	adminToolsToTest := []string{
		"admin_list_tiles",
		"admin_save_tile",
		"admin_update_tile_fields",
		"admin_delete_tile",
		"save_content_file",
		"manage_media",
		"manage_api_tokens",
	}
	for _, toolName := range adminToolsToTest {
		_, err = s.ExecuteTool(context.Background(), toolName, map[string]any{"id": 1}, false)
		if err == nil {
			t.Fatalf("expected error calling %s unauthenticated, got nil", toolName)
		}
		if !strings.Contains(err.Error(), "unauthorized") {
			t.Errorf("expected unauthorized error for %s, got %v", toolName, err)
		}
	}
}

func TestMCPContentAndConfigTools(t *testing.T) {
	tmpDir := t.TempDir()
	contentDir := filepath.Join(tmpDir, "content")
	_ = os.MkdirAll(contentDir, 0755)

	cfg := &config.Config{
		Server: config.ServerConfig{WebDir: tmpDir},
	}
	s := NewServer(cfg, nil, nil, nil, nil, nil)

	// 1. Save content file
	saveRes, err := s.ExecuteTool(context.Background(), "save_content_file", map[string]any{
		"file":    "test_de.html",
		"content": "<p>Hello World</p>",
	}, true)
	if err != nil {
		t.Fatalf("save_content_file failed: %v", err)
	}
	m := saveRes.(map[string]any)
	mtimeVal, ok := m["mtime"].(int64)
	if !ok || mtimeVal <= 0 {
		t.Fatalf("expected valid mtime, got %v", m["mtime"])
	}

	// 2. Get content file
	getRes, err := s.ExecuteTool(context.Background(), "get_content_file", map[string]any{
		"file": "test_de.html",
	}, true)
	if err != nil {
		t.Fatalf("get_content_file failed: %v", err)
	}
	getMap := getRes.(map[string]any)
	if getMap["content"] != "<p>Hello World</p>" {
		t.Errorf("unexpected content: %v", getMap["content"])
	}

	// 3. Save & Get frontend config
	_, err = s.ExecuteTool(context.Background(), "save_frontend_config", map[string]any{
		"config_json": `{"brand":"Leben Test"}`,
	}, true)
	if err != nil {
		t.Fatalf("save_frontend_config failed: %v", err)
	}

	cfgRes, err := s.ExecuteTool(context.Background(), "get_frontend_config", nil, true)
	if err != nil {
		t.Fatalf("get_frontend_config failed: %v", err)
	}
	cfgMap := cfgRes.(map[string]any)
	if cfgMap["status"] != "success" {
		t.Errorf("unexpected cfgMap: %v", cfgMap)
	}
}

func TestMCPMediaManagement(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := &config.Config{
		Server: config.ServerConfig{WebDir: tmpDir},
	}
	s := NewServer(cfg, nil, nil, nil, nil, nil)

	// 1. Upload media (asset)
	upRes, err := s.ExecuteTool(context.Background(), "manage_media", map[string]any{
		"kind":   "asset",
		"action": "upload",
		"name":   "doc.txt",
		"data":   "Hello Document Asset",
	}, true)
	if err != nil {
		t.Fatalf("manage_media upload failed: %v", err)
	}
	if !strings.Contains(upRes.(map[string]any)["message"].(string), "Uploaded") {
		t.Errorf("unexpected upload response: %v", upRes)
	}

	// 2. List media
	listRes, err := s.ExecuteTool(context.Background(), "manage_media", map[string]any{
		"kind":   "asset",
		"action": "list",
	}, true)
	if err != nil {
		t.Fatalf("manage_media list failed: %v", err)
	}
	files := listRes.(map[string]any)["files"].([]map[string]any)
	if len(files) != 1 || files[0]["name"] != "doc.txt" {
		t.Errorf("unexpected files in list: %v", files)
	}

	// 3. Rename media
	_, err = s.ExecuteTool(context.Background(), "manage_media", map[string]any{
		"kind":     "asset",
		"action":   "rename",
		"name":     "doc.txt",
		"new_name": "renamed_doc.txt",
	}, true)
	if err != nil {
		t.Fatalf("manage_media rename failed: %v", err)
	}

	// 4. Delete media
	_, err = s.ExecuteTool(context.Background(), "manage_media", map[string]any{
		"kind":   "asset",
		"action": "delete",
		"name":   "renamed_doc.txt",
	}, true)
	if err != nil {
		t.Fatalf("manage_media delete failed: %v", err)
	}
}

func TestStreamableHTTPHandleMessage(t *testing.T) {
	s := NewServer(&config.Config{}, nil, nil, nil, nil, nil)

	// 1. JSON-RPC initialize over POST returning JSON
	body := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05"}}`
	req := httptest.NewRequest(http.MethodPost, "/api/mcp", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	s.HandleMessage(rec, req, false, "https://leben.8bj.de")

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if !strings.Contains(rec.Header().Get("Content-Type"), "application/json") {
		t.Errorf("expected application/json Content-Type, got %s", rec.Header().Get("Content-Type"))
	}
	if rec.Header().Get("Mcp-Session-Id") == "" {
		t.Errorf("expected Mcp-Session-Id header to be generated")
	}
	if !strings.Contains(rec.Header().Get("WWW-Authenticate"), "oauth-protected-resource/api/mcp") {
		t.Errorf("expected WWW-Authenticate header with resource metadata for unauthenticated request")
	}

	var resp models.MCPResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if resp.ID != float64(1) && resp.ID != 1 {
		t.Errorf("expected ID 1, got %v", resp.ID)
	}

	// 2. notifications/initialized returns 204 No Content
	notifBody := `{"jsonrpc":"2.0","method":"notifications/initialized"}`
	notifReq := httptest.NewRequest(http.MethodPost, "/api/mcp", bytes.NewBufferString(notifBody))
	notifReq.Header.Set("Content-Type", "application/json")
	notifRec := httptest.NewRecorder()

	s.HandleMessage(notifRec, notifReq, false, "https://leben.8bj.de")
	if notifRec.Code != http.StatusNoContent {
		t.Fatalf("expected 204 No Content for notification, got %d", notifRec.Code)
	}

	// 3. Accept: text/event-stream on POST returns SSE stream
	streamReq := httptest.NewRequest(http.MethodPost, "/api/mcp", bytes.NewBufferString(`{"jsonrpc":"2.0","id":3,"method":"ping"}`))
	streamReq.Header.Set("Content-Type", "application/json")
	streamReq.Header.Set("Accept", "text/event-stream")
	streamRec := httptest.NewRecorder()

	s.HandleMessage(streamRec, streamReq, true, "https://leben.8bj.de")
	if !strings.Contains(streamRec.Header().Get("Content-Type"), "text/event-stream") {
		t.Errorf("expected text/event-stream, got %s", streamRec.Header().Get("Content-Type"))
	}
	if !strings.Contains(streamRec.Body.String(), "event: message\ndata:") {
		t.Errorf("expected SSE event: message format, got: %s", streamRec.Body.String())
	}
}

func TestHandleSSEEndpointBugFix(t *testing.T) {
	s := NewServer(&config.Config{}, nil, nil, nil, nil, nil)

	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest(http.MethodGet, "/api/mcp?action=sse", nil).WithContext(ctx)
	req.Host = "leben.8bj.de"
	rec := httptest.NewRecorder()

	cancel()
	s.HandleSSE(rec, req, "https://leben.8bj.de")

	body := rec.Body.String()
	if !strings.Contains(body, "event: endpoint\ndata: https://leben.8bj.de/api/mcp?action=message&sessionId=") {
		t.Errorf("expected SSE endpoint to contain /api/mcp, got: %s", body)
	}
}

func TestMCPAdminEndpointForceShowsAllTools(t *testing.T) {
	s := NewServer(&config.Config{}, nil, nil, nil, nil, nil)

	// tools/list on /api/admin/mcp unauthenticated
	body := `{"jsonrpc":"2.0","id":10,"method":"tools/list"}`
	req := httptest.NewRequest(http.MethodPost, "/api/admin/mcp", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	s.HandleMessage(rec, req, false, "https://leben.8bj.de", true)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if !strings.Contains(rec.Header().Get("WWW-Authenticate"), "oauth-protected-resource/api/admin/mcp") {
		t.Errorf("expected admin metadata URI in WWW-Authenticate, got: %s", rec.Header().Get("WWW-Authenticate"))
	}

	var resp models.MCPResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	resMap, ok := resp.Result.(map[string]any)
	if !ok {
		t.Fatalf("expected result map, got %T", resp.Result)
	}
	tools, ok := resMap["tools"].([]any)
	if !ok || len(tools) < 20 {
		t.Fatalf("expected all 24 tools on /api/admin/mcp, got %d", len(tools))
	}

	// Calling admin tool on /api/admin/mcp unauthenticated must fail
	callBody := `{"jsonrpc":"2.0","id":11,"method":"tools/call","params":{"name":"admin_list_tiles","arguments":{}}}`
	callReq := httptest.NewRequest(http.MethodPost, "/api/admin/mcp", bytes.NewBufferString(callBody))
	callReq.Header.Set("Content-Type", "application/json")
	callRec := httptest.NewRecorder()

	s.HandleMessage(callRec, callReq, false, "https://leben.8bj.de", true)
	var callResp models.MCPResponse
	_ = json.Unmarshal(callRec.Body.Bytes(), &callResp)
	callResult, _ := callResp.Result.(map[string]any)
	if callResult["isError"] != true {
		t.Errorf("expected isError=true for unauthenticated admin tool call, got %v", callResult)
	}
}

func TestMCPLimitZeroHandling(t *testing.T) {
	// 1. Unset limit -> default 20
	argsUnset := map[string]any{"q": "test"}
	if l := getIntOpt(argsUnset, "limit", 20); l != 20 {
		t.Errorf("expected 20 for unset limit, got %d", l)
	}

	// 2. Explicit 0 -> returns 0 (which means all)
	argsZero := map[string]any{"limit": 0}
	if l := getIntOpt(argsZero, "limit", 20); l != 0 {
		t.Errorf("expected 0 for explicit limit=0, got %d", l)
	}

	// 3. Explicit float64(0) (JSON unmarshal default)
	argsFloatZero := map[string]any{"limit": float64(0)}
	if l := getIntOpt(argsFloatZero, "limit", 20); l != 0 {
		t.Errorf("expected 0 for float64(0) limit, got %d", l)
	}
}

func TestPostgresToTagsAndTagsToPostgresQuotes(t *testing.T) {
	// Simulating PostgreSQL array serialization containing multi-word tag with space
	pgArr := `{finance,crypto,investments,"passive income"}`
	parsed := db.PostgresToTags(pgArr)
	expected := "finance, crypto, investments, passive income"
	if parsed != expected {
		t.Errorf("expected %q, got %q", expected, parsed)
	}

	// Simulating tags with literal quotes converted to Postgres array
	rawTags := `finance, crypto, investments, "passive income"`
	saved := db.TagsToPostgres(rawTags)
	expectedPg := "{finance,crypto,investments,passive income}"
	if saved != expectedPg {
		t.Errorf("expected %q, got %q", expectedPg, saved)
	}
}
