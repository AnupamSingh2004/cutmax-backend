package handlers

import (
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/h2non/filetype"

	"github.com/cutmax/cutmax-backend/internal/config"
	"github.com/cutmax/cutmax-backend/internal/db"
	"github.com/cutmax/cutmax-backend/internal/util"
)

// ===== Admin Single Upload =====

func HandleAdminUpload(w http.ResponseWriter, r *http.Request) {
	r.ParseMultipartForm(int64(config.Cfg.MaxUploadMB) << 20)
	file, fh, err := r.FormFile("image")
	sku := r.FormValue("sku")
	if err != nil || sku == "" {
		util.JsonErr(w, 400, "'image' and 'sku' required")
		return
	}
	defer file.Close()

	data, _ := io.ReadAll(file)
	_, ext, ok := sniffFile(data)
	if !ok {
		util.JsonErr(w, 422, "Unsupported image type (jpeg/png/webp/gif only)")
		return
	}

	// Find product
	var pid, oldURL *string
	err = db.Pool.QueryRow(r.Context(), "SELECT id,image_url FROM products WHERE sku=$1", sku).Scan(&pid, &oldURL)
	if err != nil {
		util.JsonErr(w, 404, "Product not found")
		return
	}

	key := buildKey(sku, ext)
	url, err := saveFile(key, data)
	if err != nil {
		util.JsonErr(w, 500, err.Error())
		return
	}

	if oldURL != nil && *oldURL != "" {
		oldKey := keyFromURL(*oldURL)
		if oldKey != "" {
			os.Remove(filepath.Join(config.Cfg.UploadsDir, oldKey))
		}
	}

	db.Pool.Exec(r.Context(), "UPDATE products SET image_url=$1,image_type='UPLOADED',updated_at=NOW() WHERE id=$2", url, pid)
	_ = fh
	util.JsonOK(w, 200, map[string]interface{}{
		"product": map[string]interface{}{"id": pid, "imageUrl": url, "imageType": "UPLOADED"},
	})
}

// ===== Serve Uploads =====

func HandleServeUpload(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(r.URL.Path, "/")
	if len(parts) < 2 {
		http.NotFound(w, r)
		return
	}
	filename := parts[len(parts)-1]
	if strings.Contains(filename, "..") {
		http.NotFound(w, r)
		return
	}
	ext := filepath.Ext(filename)
	extToMime := map[string]string{".jpg": "image/jpeg", ".jpeg": "image/jpeg", ".png": "image/png", ".webp": "image/webp", ".gif": "image/gif"}
	mimeType, ok := extToMime[ext]
	if !ok {
		http.NotFound(w, r)
		return
	}
	target := filepath.Join(config.Cfg.UploadsDir, filename)
	data, err := os.ReadFile(target)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", mimeType)
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	w.Write(data)
}

func sniffFile(data []byte) (mime, ext string, ok bool) {
	kind, _ := filetype.Match(data)
	extMap := map[string]string{"image/jpeg": "jpg", "image/png": "png", "image/webp": "webp", "image/gif": "gif"}
	e, found := extMap[kind.MIME.Value]
	if !found {
		return "", "", false
	}
	return kind.MIME.Value, e, true
}

func buildKey(sku, ext string) string {
	safe := regexp.MustCompile(`[^A-Za-z0-9_-]`).ReplaceAllString(sku, "_")
	if len(safe) > 64 {
		safe = safe[:64]
	}
	b := make([]byte, 8)
	rand.Read(b)
	return fmt.Sprintf("%s_%x.%s", safe, b, ext)
}

func saveFile(key string, data []byte) (string, error) {
	os.MkdirAll(config.Cfg.UploadsDir, 0755)
	target := filepath.Join(config.Cfg.UploadsDir, key)
	if err := os.WriteFile(target, data, 0644); err != nil {
		return "", err
	}
	return config.Cfg.UploadsPublicBaseURL + "/" + key, nil
}

func keyFromURL(url string) string {
	base := config.Cfg.UploadsPublicBaseURL
	if !strings.HasPrefix(url, base) {
		return ""
	}
	return strings.TrimPrefix(strings.TrimPrefix(url, base), "/")
}
