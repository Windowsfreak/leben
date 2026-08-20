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

	// 2. Test POST /api/admin/images/upload (small file <= 50KB with special characters)
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	part, err := writer.CreateFormFile("image", "my custom photo! 2026.webp")
	if err != nil {
		t.Fatalf("failed to create form file: %v", err)
	}
	_, _ = io.WriteString(part, "small webp content")
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

	// 3. Test POST /api/admin/images/upload with large PNG (> 50KB) converted to WebP
	img := image.NewRGBA(image.Rect(0, 0, 800, 800))
	var seed uint32 = 12345
	for y := 0; y < 800; y++ {
		for x := 0; x < 800; x++ {
			seed = seed*1664525 + 1013904223
			img.Set(x, y, color.RGBA{R: uint8(seed), G: uint8(seed >> 8), B: uint8(seed >> 16), A: 255})
		}
	}
	var pngBuf bytes.Buffer
	if err := png.Encode(&pngBuf, img); err != nil {
		t.Fatalf("failed to encode png: %v", err)
	}
	if pngBuf.Len() <= 51200 {
		t.Fatalf("test png size is %d, expected > 51200", pngBuf.Len())
	}

	largeBody := &bytes.Buffer{}
	largeWriter := multipart.NewWriter(largeBody)
	largePart, err := largeWriter.CreateFormFile("image", "large_sample.png")
	if err != nil {
		t.Fatalf("failed to create large form file: %v", err)
	}
	_, _ = io.Copy(largePart, &pngBuf)
	largeWriter.Close()

	largeReq := httptest.NewRequest(http.MethodPost, "/api/admin/images/upload", largeBody)
	largeReq.Header.Set("Authorization", "Bearer "+secretToken)
	largeReq.Header.Set("Content-Type", largeWriter.FormDataContentType())
	largeW := httptest.NewRecorder()
	r.ServeHTTP(largeW, largeReq)

	if largeW.Code != http.StatusOK {
		t.Fatalf("large upload expected 200 OK, got %d: %s", largeW.Code, largeW.Body.String())
	}

	var largeRes struct {
		Status string `json:"status"`
		Name   string `json:"name"`
	}
	_ = json.Unmarshal(largeW.Body.Bytes(), &largeRes)
	if filepath.Ext(largeRes.Name) != ".webp" {
		t.Fatalf("expected converted .webp extension for large image, got '%s'", largeRes.Name)
	}

	// 4. Test POST /api/admin/images/rename
	renameBody := `{"old_name": "my_custom_photo__2026.webp", "new_name": "renamed_tile.webp"}`
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

	// 5. Test DELETE /api/admin/images/:name
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
