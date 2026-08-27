// Package storage abstracts where uploaded files (product images, media
// library images/video) end up — local disk for zero-config local dev, or
// an S3-compatible bucket (Cloudflare R2) in production.
package storage

import (
	"context"

	"github.com/cutmax/cutmax-backend/internal/config"
)

type Storage interface {
	// Save writes data under key and returns its public URL.
	Save(ctx context.Context, key string, data []byte, contentType string) (url string, err error)
	// Delete removes the object at key. Not finding it is not an error.
	Delete(ctx context.Context, key string) error
}

// Active is the process-wide storage backend, set once at startup by cmd/server.
var Active Storage

// New builds the Storage implementation selected by config.Cfg.StorageDriver.
func New() (Storage, error) {
	if config.Cfg.StorageDriver == "s3" {
		return newS3(config.Cfg)
	}
	return newLocal(config.Cfg), nil
}
