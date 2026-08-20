package router

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/windowsfreak/leben/internal/auth"
	"github.com/windowsfreak/leben/internal/config"
)

func TestRouterInitialization(t *testing.T) {
	cfg := &config.Config{
		Server: config.ServerConfig{WebDir: "."},
	}
	r := New(cfg, nil, nil, nil, nil, nil, nil)
	if r == nil {
		t.Fatal("expected non-nil router")
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
	if err := os.WriteFile(dummyFile, []byte("test image content"), 0644); err != nil {
		t.Fatalf("failed to write dummy file: %v", err)
	}

	secretToken := "test_secret_admin_token"
	cfg := &config.Config{
		Server: config.ServerConfig{WebDir: tmpDir},
		Admin:  config.AdminConfig{SecretToken: secretToken},
	}
	authModule := auth.New(cfg)
	r := New(cfg, authModule, nil, nil, nil, nil, nil)

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

	// 2. Test POST /api/admin/images/upload
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	part, err := writer.CreateFormFile("image", "uploaded_tile.webp")
	if err != nil {
		t.Fatalf("failed to create form file: %v", err)
	}
	_, _ = io.WriteString(part, "uploaded content")
	writer.Close()

	uploadReq := httptest.NewRequest(http.MethodPost, "/api/admin/images/upload", body)
	uploadReq.Header.Set("Authorization", "Bearer "+secretToken)
	uploadReq.Header.Set("Content-Type", writer.FormDataContentType())
	uploadW := httptest.NewRecorder()
	r.ServeHTTP(uploadW, uploadReq)

	if uploadW.Code != http.StatusOK {
		t.Fatalf("upload expected 200 OK, got %d: %s", uploadW.Code, uploadW.Body.String())
	}

	uploadedFilePath := filepath.Join(tileimgDir, "uploaded_tile.webp")
	if _, err := os.Stat(uploadedFilePath); os.IsNotExist(err) {
		t.Fatalf("uploaded file not found in tileimg directory: %s", uploadedFilePath)
	}

	// 3. Test POST /api/admin/images/rename
	renameBody := `{"old_name": "uploaded_tile.webp", "new_name": "renamed_tile.webp"}`
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

	// 4. Test DELETE /api/admin/images/:name
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
