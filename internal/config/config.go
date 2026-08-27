package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/joho/godotenv"
)

type Config struct {
	DatabaseURL          string
	RedisURL             string
	CustomerJWTSecret    string
	AdminJWTSecret       string
	AllowedOrigins       []string
	UploadsDir           string
	UploadsPublicBaseURL string
	MaxUploadMB          int
	EmailProvider        string
	SMTPHost             string
	SMTPPort             int
	SMTPFrom             string
	NodeEnv              string
	Port                 int
}

var Cfg Config

func LoadConfig() error {
	_ = godotenv.Load(".env")
	_ = godotenv.Load(".env.test")

	Cfg = Config{
		DatabaseURL:          env("DATABASE_URL"),
		RedisURL:             env("REDIS_URL"),
		CustomerJWTSecret:    env("CUSTOMER_JWT_SECRET"),
		AdminJWTSecret:       env("ADMIN_JWT_SECRET"),
		AllowedOrigins:       split(env("ALLOWED_ORIGINS")),
		UploadsDir:           envOr("UPLOADS_DIR", "./uploads"),
		UploadsPublicBaseURL: envOr("UPLOADS_PUBLIC_BASE_URL", "http://localhost:3000/api/uploads"),
		MaxUploadMB:          envIntOr("MAX_UPLOAD_MB", 5),
		EmailProvider:        envOr("EMAIL_PROVIDER", "smtp"),
		SMTPHost:             envOr("SMTP_HOST", "localhost"),
		SMTPPort:             envIntOr("SMTP_PORT", 1025),
		SMTPFrom:             envOr("SMTP_FROM", "CutMax Technologies <no-reply@cutmax.local>"),
		NodeEnv:              envOr("NODE_ENV", "development"),
		Port:                 envIntOr("PORT", 3000),
	}
	if Cfg.DatabaseURL == "" {
		return fmt.Errorf("DATABASE_URL is required")
	}
	if len(Cfg.CustomerJWTSecret) < 32 || len(Cfg.AdminJWTSecret) < 32 {
		return fmt.Errorf("JWT secrets must be >= 32 chars")
	}
	if len(Cfg.AllowedOrigins) == 0 {
		return fmt.Errorf("ALLOWED_ORIGINS is required")
	}
	return nil
}

func env(key string) string { return os.Getenv(key) }
func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
func envIntOr(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if i, err := strconv.Atoi(v); err == nil {
			return i
		}
	}
	return def
}
func split(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}
