package handlers

import (
	"io"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/h2non/filetype"

	"github.com/cutmax/cutmax-backend/internal/config"
	"github.com/cutmax/cutmax-backend/internal/db"
	"github.com/cutmax/cutmax-backend/internal/storage"
	"github.com/cutmax/cutmax-backend/internal/util"
)

type mediaAsset struct {
	ID          string    `json:"id"`
	Key         string    `json:"key"`
	URL         string    `json:"url"`
	Kind        string    `json:"kind"`
	Filename    string    `json:"filename"`
	ContentType string    `json:"contentType"`
	SizeBytes   int64     `json:"sizeBytes"`
	CreatedAt   time.Time `json:"createdAt"`
}

// sniffMedia detects both image and video types by content (magic bytes),
// unlike sniffFile in handlers_upload.go which is deliberately image-only
// (product photos shouldn't accept video).
func sniffMedia(data []byte) (mime, ext, kind string, ok bool) {
	k, _ := filetype.Match(data)
	imageExt := map[string]string{"image/jpeg": "jpg", "image/png": "png", "image/webp": "webp", "image/gif": "gif"}
	videoExt := map[string]string{"video/mp4": "mp4", "video/webm": "webm", "video/quicktime": "mov"}
	if e, found := imageExt[k.MIME.Value]; found {
		return k.MIME.Value, e, "IMAGE", true
	}
	if e, found := videoExt[k.MIME.Value]; found {
		return k.MIME.Value, e, "VIDEO", true
	}
	return "", "", "", false
}

// ===== Admin Media Library =====

func HandleAdminMediaList(w http.ResponseWriter, r *http.Request) {
	rows, err := db.Pool.Query(r.Context(),
		"SELECT id,key,url,kind,filename,content_type,size_bytes,created_at FROM media_assets ORDER BY created_at DESC")
	if err != nil {
		util.JsonErr(w, 500, err.Error())
		return
	}
	defer rows.Close()
	assets := []mediaAsset{}
	for rows.Next() {
		var a mediaAsset
		rows.Scan(&a.ID, &a.Key, &a.URL, &a.Kind, &a.Filename, &a.ContentType, &a.SizeBytes, &a.CreatedAt)
		assets = append(assets, a)
	}
	util.JsonOK(w, 200, map[string]interface{}{"assets": assets})
}

func HandleAdminMediaUpload(w http.ResponseWriter, r *http.Request) {
	r.ParseMultipartForm(int64(config.Cfg.MaxUploadMB) << 20)
	file, fh, err := r.FormFile("file")
	if err != nil {
		util.JsonErr(w, 400, "'file' is required")
		return
	}
	defer file.Close()

	data, _ := io.ReadAll(file)
	mime, ext, kind, ok := sniffMedia(data)
	if !ok {
		util.JsonErr(w, 422, "Unsupported file type (jpeg/png/webp/gif/mp4/webm/mov only)")
		return
	}

	key := buildKey(kind, ext)
	url, err := storage.Active.Save(r.Context(), key, data, mime)
	if err != nil {
		util.JsonErr(w, 500, err.Error())
		return
	}

	a := mediaAsset{
		ID: uuid.New().String(), Key: key, URL: url, Kind: kind,
		Filename: fh.Filename, ContentType: mime, SizeBytes: fh.Size,
	}
	err = db.Pool.QueryRow(r.Context(),
		`INSERT INTO media_assets (id,key,url,kind,filename,content_type,size_bytes,created_at)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,NOW()) RETURNING created_at`,
		a.ID, a.Key, a.URL, a.Kind, a.Filename, a.ContentType, a.SizeBytes,
	).Scan(&a.CreatedAt)
	if err != nil {
		storage.Active.Delete(r.Context(), key)
		util.JsonErr(w, 500, err.Error())
		return
	}
	util.JsonOK(w, 201, map[string]interface{}{"asset": a})
}

func HandleAdminMediaDelete(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var key string
	err := db.Pool.QueryRow(r.Context(), "SELECT key FROM media_assets WHERE id=$1", id).Scan(&key)
	if err != nil {
		util.JsonErr(w, 404, "Asset not found")
		return
	}
	storage.Active.Delete(r.Context(), key)
	db.Pool.Exec(r.Context(), "DELETE FROM media_assets WHERE id=$1", id)
	util.JsonOK(w, 200, map[string]interface{}{"message": "Deleted"})
}
