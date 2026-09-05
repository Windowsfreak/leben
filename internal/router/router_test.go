package router

import (
	"bytes"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/windowsfreak/leben/internal/auth"
	"github.com/windowsfreak/leben/internal/config"
	"github.com/windowsfreak/leben/internal/mcp"
	"github.com/windowsfreak/leben/internal/models"
)

func TestRouterInitialization(t *testing.T) {
	cfg := &config.Config{
		Server: config.ServerConfig{WebDir: "."},
	}
	r := New(cfg, nil, nil, nil, nil, nil, nil, nil)
	if r == nil {
		t.Fatal("expected non-nil router")
	}

	// /api/healthz must respond 503 degraded without panicking when DB is nil
	req := httptest.NewRequest(http.MethodGet, "/api/healthz", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected status 503 for nil DB health check, got %d", rec.Code)
	}
}

func TestAdminTileImageEndpoints(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "leben-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	tileimgDir := filepath.Join(tmpDir, "tileimg")
	if err := os.MkdirAll(tileimgDir, 0755); err != nil {
		t.Fatalf("failed to create tileimg dir: %v", err)
	}

	// Create a dummy image in tileimg
	dummyFile := filepath.Join(tileimgDir, "test_tile_bg.webp")
	if err := os.WriteFile(dummyFile, []byte("RIFF____WEBPtest image content"), 0644); err != nil {
		t.Fatalf("failed to write dummy file: %v", err)
	}

	secretToken := "test_secret_admin_token"
	cfg := &config.Config{
		Server: config.ServerConfig{WebDir: tmpDir},
		Admin:  config.AdminConfig{},
	}
	authModule := auth.New(cfg, nil)
	authModule.SetTestToken(secretToken)
	r := New(cfg, authModule, nil, nil, nil, nil, nil, nil)

	// 1. Test GET /api/admin/images (List)
	req := httptest.NewRequest(http.MethodGet, "/api/admin/images", nil)
	req.Header.Set("Authorization", "Bearer "+secretToken)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d: %s", w.Code, w.Body.String())
	}

	var listRes struct {
		Status string `json:"status"`
		Images []struct {
			Name string `json:"name"`
			URL  string `json:"url"`
		} `json:"images"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &listRes); err != nil {
		t.Fatalf("failed to parse json: %v", err)
	}
	if listRes.Status != "success" {
		t.Fatalf("expected status success, got %s", listRes.Status)
	}
	if len(listRes.Images) != 1 || listRes.Images[0].Name != "test_tile_bg.webp" {
		t.Fatalf("unexpected images list: %+v", listRes.Images)
	}
	if listRes.Images[0].URL != "./tileimg/test_tile_bg.webp" {
		t.Fatalf("expected URL './tileimg/test_tile_bg.webp', got '%s'", listRes.Images[0].URL)
	}

	// 2. Test POST /api/admin/images/upload (small WebP file <= 100KB: kept unmodified)
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	part, err := writer.CreateFormFile("image", "my custom photo! 2026.webp")
	if err != nil {
		t.Fatalf("failed to create form file: %v", err)
	}
	_, _ = io.WriteString(part, "RIFF____WEBPsmall webp content")
	writer.Close()

	uploadReq := httptest.NewRequest(http.MethodPost, "/api/admin/images/upload", body)
	uploadReq.Header.Set("Authorization", "Bearer "+secretToken)
	uploadReq.Header.Set("Content-Type", writer.FormDataContentType())
	uploadW := httptest.NewRecorder()
	r.ServeHTTP(uploadW, uploadReq)

	if uploadW.Code != http.StatusOK {
		t.Fatalf("upload expected 200 OK, got %d: %s", uploadW.Code, uploadW.Body.String())
	}

	var uploadRes struct {
		Status string `json:"status"`
		Name   string `json:"name"`
		Image  struct {
			Name string `json:"name"`
			URL  string `json:"url"`
		} `json:"image"`
	}
	if err := json.Unmarshal(uploadW.Body.Bytes(), &uploadRes); err != nil {
		t.Fatalf("failed to parse upload json: %v", err)
	}
	expectedName := "my_custom_photo__2026.webp"
	if uploadRes.Name != expectedName {
		t.Fatalf("expected sanitized name '%s', got '%s'", expectedName, uploadRes.Name)
	}

	uploadedFilePath := filepath.Join(tileimgDir, expectedName)
	if _, err := os.Stat(uploadedFilePath); os.IsNotExist(err) {
		t.Fatalf("uploaded file not found in tileimg directory: %s", uploadedFilePath)
	}

	// 3. Test POST /api/admin/images/upload with a small PNG (<= 50KB): kept as PNG unmodified
	smallImg := image.NewRGBA(image.Rect(0, 0, 50, 50))
	for y := 0; y < 50; y++ {
		for x := 0; x < 50; x++ {
			smallImg.Set(x, y, color.RGBA{R: 200, G: 100, B: 50, A: 255})
		}
	}
	var smallPngBuf bytes.Buffer
	if err := png.Encode(&smallPngBuf, smallImg); err != nil {
		t.Fatalf("failed to encode png: %v", err)
	}

	smallPngBody := &bytes.Buffer{}
	smallPngWriter := multipart.NewWriter(smallPngBody)
	smallPngPart, err := smallPngWriter.CreateFormFile("image", "small_card.png")
	if err != nil {
		t.Fatalf("failed to create png form file: %v", err)
	}
	_, _ = io.Copy(smallPngPart, &smallPngBuf)
	smallPngWriter.Close()

	smallPngReq := httptest.NewRequest(http.MethodPost, "/api/admin/images/upload", smallPngBody)
	smallPngReq.Header.Set("Authorization", "Bearer "+secretToken)
	smallPngReq.Header.Set("Content-Type", smallPngWriter.FormDataContentType())
	smallPngW := httptest.NewRecorder()
	r.ServeHTTP(smallPngW, smallPngReq)

	if smallPngW.Code != http.StatusOK {
		t.Fatalf("small png upload expected 200 OK, got %d: %s", smallPngW.Code, smallPngW.Body.String())
	}

	var smallPngRes struct {
		Status string `json:"status"`
		Name   string `json:"name"`
	}
	_ = json.Unmarshal(smallPngW.Body.Bytes(), &smallPngRes)
	if smallPngRes.Name != "small_card.png" {
		t.Fatalf("expected output name 'small_card.png' (<=50KB unmodified), got '%s'", smallPngRes.Name)
	}

	// 4. Test POST /api/admin/images/upload with large PNG (> 50KB): converted to WebP format
	largeImg := image.NewRGBA(image.Rect(0, 0, 800, 800))
	var seed uint32 = 12345
	for y := 0; y < 800; y++ {
		for x := 0; x < 800; x++ {
			seed = seed*1664525 + 1013904223
			largeImg.Set(x, y, color.RGBA{R: uint8(seed), G: uint8(seed >> 8), B: uint8(seed >> 16), A: 255})
		}
	}
	var largePngBuf bytes.Buffer
	if err := png.Encode(&largePngBuf, largeImg); err != nil {
		t.Fatalf("failed to encode png: %v", err)
	}
	if largePngBuf.Len() <= 51200 {
		t.Fatalf("expected large png size > 51200, got %d", largePngBuf.Len())
	}

	largePngBody := &bytes.Buffer{}
	largePngWriter := multipart.NewWriter(largePngBody)
	largePngPart, err := largePngWriter.CreateFormFile("image", "large_card.png")
	if err != nil {
		t.Fatalf("failed to create large png form file: %v", err)
	}
	_, _ = io.Copy(largePngPart, &largePngBuf)
	largePngWriter.Close()

	largePngReq := httptest.NewRequest(http.MethodPost, "/api/admin/images/upload", largePngBody)
	largePngReq.Header.Set("Authorization", "Bearer "+secretToken)
	largePngReq.Header.Set("Content-Type", largePngWriter.FormDataContentType())
	largePngW := httptest.NewRecorder()
	r.ServeHTTP(largePngW, largePngReq)

	if largePngW.Code != http.StatusOK {
		t.Fatalf("large png upload expected 200 OK, got %d: %s", largePngW.Code, largePngW.Body.String())
	}

	var largePngRes struct {
		Status string `json:"status"`
		Name   string `json:"name"`
	}
	_ = json.Unmarshal(largePngW.Body.Bytes(), &largePngRes)
	if largePngRes.Name != "large_card.webp" {
		t.Fatalf("expected converted output name 'large_card.webp', got '%s'", largePngRes.Name)
	}

	convertedData, err := os.ReadFile(filepath.Join(tileimgDir, "large_card.webp"))
	if err != nil {
		t.Fatalf("failed to read converted file: %v", err)
	}
	if len(convertedData) < 12 || string(convertedData[:4]) != "RIFF" || string(convertedData[8:12]) != "WEBP" {
		t.Fatalf("converted file is not a valid WebP! Header: %q", convertedData[:min(len(convertedData), 16)])
	}

	// 5. Test POST /api/admin/images/rename
	renameBody := `{"old_name": "large_card.webp", "new_name": "renamed_tile.webp"}`
	renameReq := httptest.NewRequest(http.MethodPost, "/api/admin/images/rename", bytes.NewBufferString(renameBody))
	renameReq.Header.Set("Authorization", "Bearer "+secretToken)
	renameReq.Header.Set("Content-Type", "application/json")
	renameW := httptest.NewRecorder()
	r.ServeHTTP(renameW, renameReq)

	if renameW.Code != http.StatusOK {
		t.Fatalf("rename expected 200 OK, got %d: %s", renameW.Code, renameW.Body.String())
	}

	renamedFilePath := filepath.Join(tileimgDir, "renamed_tile.webp")
	if _, err := os.Stat(renamedFilePath); os.IsNotExist(err) {
		t.Fatalf("renamed file not found in tileimg directory: %s", renamedFilePath)
	}

	// 6. Test DELETE /api/admin/images/:name
	deleteReq := httptest.NewRequest(http.MethodDelete, "/api/admin/images/renamed_tile.webp", nil)
	deleteReq.Header.Set("Authorization", "Bearer "+secretToken)
	deleteW := httptest.NewRecorder()
	r.ServeHTTP(deleteW, deleteReq)

	if deleteW.Code != http.StatusOK {
		t.Fatalf("delete expected 200 OK, got %d: %s", deleteW.Code, deleteW.Body.String())
	}

	if _, err := os.Stat(renamedFilePath); !os.IsNotExist(err) {
		t.Fatalf("expected deleted file to not exist in tileimg: %s", renamedFilePath)
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func TestFrontendTileSerialization(t *testing.T) {
	tile := &models.Tile{
		ID:          1,
		Name:        "test-tile",
		Language:    "de",
		Tags:        "test,demo",
		Title:       "Test Tile",
		HTMLTeaser:  "<p>Teaser</p>",
		Summary:     "Internal embedding summary",
		Visible:     true,
		AccentColor: "#3b82f6",
		Type:        "doc",
	}

	// 1. Non-admin frontend DTO
	publicDto := toFrontendTileDTO(tile, false)
	publicData, err := json.Marshal(publicDto)
	if err != nil {
		t.Fatalf("failed to marshal public TileDTO: %v", err)
	}

	var publicMap map[string]any
	if err := json.Unmarshal(publicData, &publicMap); err != nil {
		t.Fatalf("failed to unmarshal public JSON: %v", err)
	}

	forbiddenPublicKeys := []string{"id", "summary", "tags", "language", "index", "score", "visible", "secret", "sort_order"}
	for _, key := range forbiddenPublicKeys {
		if _, exists := publicMap[key]; exists {
			t.Errorf("expected key %q to be omitted from public TileDTO JSON, but it was present", key)
		}
	}

	requiredPublicKeys := []string{"name", "lang", "title", "html_teaser", "accent_color", "type"}
	for _, key := range requiredPublicKeys {
		if _, exists := publicMap[key]; !exists {
			t.Errorf("expected required key %q in public TileDTO JSON, but it was missing", key)
		}
	}

	// 2. Admin frontend DTO
	adminDto := toFrontendTileDTO(tile, true)
	adminData, err := json.Marshal(adminDto)
	if err != nil {
		t.Fatalf("failed to marshal admin TileDTO: %v", err)
	}

	var adminMap map[string]any
	if err := json.Unmarshal(adminData, &adminMap); err != nil {
		t.Fatalf("failed to unmarshal admin JSON: %v", err)
	}

	requiredAdminKeys := []string{"id", "name", "lang", "title", "visible", "tags"}
	for _, key := range requiredAdminKeys {
		if _, exists := adminMap[key]; !exists {
			t.Errorf("expected required key %q in admin TileDTO JSON, but it was missing", key)
		}
	}
}

func TestRichTileDTOSerialization(t *testing.T) {
	scoreVal := 0.85
	tile := &models.Tile{
		ID:          42,
		Name:        "deep-thought",
		Language:    "en",
		Tags:        "ai,philosophy",
		Title:       "Deep Thought",
		HTMLTeaser:  "<p>Teaser</p>",
		Summary:     "42 is the answer",
		Visible:     true,
		AccentColor: "#fbbf24",
		Type:        "doc",
		Score:       &scoreVal,
	}

	dto := toRichTileDTO(tile, 1, "summary", 0, t.TempDir(), "answer", "")
	data, err := json.Marshal(dto)
	if err != nil {
		t.Fatalf("failed to marshal rich TileDTO: %v", err)
	}

	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("failed to unmarshal JSON: %v", err)
	}

	if m["name"] != "deep-thought" || m["lang"] != "en" || m["score"] != 0.85 {
		t.Errorf("unexpected rich TileDTO values: %v", m)
	}
	if _, exists := m["distance"]; exists {
		t.Errorf("distance must not be serialized in TileDTO")
	}
	if _, exists := m["language"]; exists {
		t.Errorf("language must not be serialized in TileDTO (use lang)")
	}
}

func TestOAuthDiscoveryEndpoints(t *testing.T) {
	cfg := &config.Config{
		Server: config.ServerConfig{
			WebDir:    t.TempDir(),
			PublicURL: "https://leben.8bj.de",
		},
	}
	r := New(cfg, nil, nil, nil, nil, nil, nil, nil)

	// 1. RFC 9728 Protected Resource Metadata
	paths := []string{
		"/.well-known/oauth-protected-resource",
		"/.well-known/oauth-protected-resource/api/mcp",
		"/api/.well-known/oauth-protected-resource",
		"/api/.well-known/oauth-protected-resource/api/mcp",
	}
	for _, p := range paths {
		req := httptest.NewRequest(http.MethodGet, p, nil)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Errorf("expected 200 for %s, got %d", p, rec.Code)
		}
		var m map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
			t.Fatalf("failed to decode JSON for %s: %v", p, err)
		}
		if m["resource"] != "https://leben.8bj.de/api/mcp" {
			t.Errorf("expected resource https://leben.8bj.de/api/mcp, got %v", m["resource"])
		}
	}

	// 2. RFC 8414 Authorization Server Metadata
	authServerPaths := []string{
		"/.well-known/oauth-authorization-server",
		"/api/.well-known/oauth-authorization-server",
	}
	for _, p := range authServerPaths {
		req := httptest.NewRequest(http.MethodGet, p, nil)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Errorf("expected 200 for %s, got %d", p, rec.Code)
		}
		var m map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
			t.Fatalf("failed to decode JSON for %s: %v", p, err)
		}
		if m["issuer"] != "https://leben.8bj.de" {
			t.Errorf("expected issuer https://leben.8bj.de, got %v", m["issuer"])
		}
		if m["device_authorization_endpoint"] != "https://leben.8bj.de/api/auth/device" {
			t.Errorf("expected device_authorization_endpoint https://leben.8bj.de/api/auth/device, got %v", m["device_authorization_endpoint"])
		}
	}

	// 3. RFC 9728 Admin Protected Resource Metadata
	adminMetaPaths := []string{
		"/.well-known/oauth-protected-resource/api/admin/mcp",
		"/api/.well-known/oauth-protected-resource/api/admin/mcp",
	}
	for _, p := range adminMetaPaths {
		req := httptest.NewRequest(http.MethodGet, p, nil)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Errorf("expected 200 for %s, got %d", p, rec.Code)
		}
		var m map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
			t.Fatalf("failed to decode JSON for %s: %v", p, err)
		}
		if m["resource"] != "https://leben.8bj.de/api/admin/mcp" {
			t.Errorf("expected resource https://leben.8bj.de/api/admin/mcp, got %v", m["resource"])
		}
	}
}

func TestMCPAdminRouterEndpoints(t *testing.T) {
	cfg := &config.Config{
		Server: config.ServerConfig{
			WebDir:    t.TempDir(),
			PublicURL: "https://leben.8bj.de",
		},
	}
	mcpServer := mcp.NewServer(cfg, nil, nil, nil, nil, nil)
	r := New(cfg, nil, nil, nil, nil, nil, nil, mcpServer)

	// POST /api/admin/mcp with tools/list returns all tools even when unauthenticated
	body := `{"jsonrpc":"2.0","id":100,"method":"tools/list"}`
	req := httptest.NewRequest(http.MethodPost, "/api/admin/mcp", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
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
}

