package storage

import (
	"context"
	"os"
	"path/filepath"

	"github.com/cutmax/cutmax-backend/internal/config"
)

type localStorage struct {
	dir       string
	publicURL string
}

func newLocal(cfg config.Config) *localStorage {
	return &localStorage{dir: cfg.UploadsDir, publicURL: cfg.UploadsPublicBaseURL}
}

func (s *localStorage) Save(_ context.Context, key string, data []byte, _ string) (string, error) {
	os.MkdirAll(s.dir, 0755)
	target := filepath.Join(s.dir, key)
	if err := os.WriteFile(target, data, 0644); err != nil {
		return "", err
	}
	return s.publicURL + "/" + key, nil
}

func (s *localStorage) Delete(_ context.Context, key string) error {
	err := os.Remove(filepath.Join(s.dir, key))
	if os.IsNotExist(err) {
		return nil
	}
	return err
}
