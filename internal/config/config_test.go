package config

import (
	"os"
	"testing"
)

func TestLoadConfig(t *testing.T) {
	// Test that loadConfig fails without required env vars
	origEnv := os.Environ()
	// Clear all env vars
	for _, e := range origEnv {
		k := e[:len(e)-len(e[len(e)-1:])]
		for i := len(e) - 1; i >= 0; i-- {
			if e[i] == '=' {
				k = e[:i]
				break
			}
		}
		os.Unsetenv(k)
	}
	defer func() {
		for _, e := range origEnv {
			k := e[:len(e)-len(e[len(e)-1:])]
			for i := len(e) - 1; i >= 0; i-- {
				if e[i] == '=' {
					k = e[:i]
					break
				}
			}
			os.Setenv(k, e[len(k)+1:])
		}
	}()

	os.Setenv("DATABASE_URL", "postgresql://test:test@localhost/test")
	os.Setenv("CUSTOMER_JWT_SECRET", "short")
	os.Setenv("ADMIN_JWT_SECRET", "also-short")
	os.Setenv("ALLOWED_ORIGINS", "http://localhost:3001")

	err := LoadConfig()
	if err == nil {
		t.Error("LoadConfig should fail with short JWT secrets")
	}
}
