package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/cutmax/cutmax-backend/internal/config"
)

func TestClientIP(t *testing.T) {
	tests := []struct {
		name     string
		headers  map[string]string
		remote   string
		expected string
	}{
		{"X-Forwarded-For", map[string]string{"X-Forwarded-For": "1.2.3.4, 5.6.7.8"}, "10.0.0.1:8080", "1.2.3.4"},
		{"X-Real-IP", map[string]string{"X-Real-IP": "9.8.7.6"}, "10.0.0.1:8080", "9.8.7.6"},
		{"RemoteAddr with port", nil, "192.168.1.1:9090", "192.168.1.1"},
		{"RemoteAddr no port", nil, "192.168.1.1", "192.168.1.1"},
		{"XFF takes priority", map[string]string{"X-Forwarded-For": "1.1.1.1", "X-Real-IP": "2.2.2.2"}, "10.0.0.1:80", "1.1.1.1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, _ := http.NewRequest("GET", "/", nil)
			for k, v := range tt.headers {
				req.Header.Set(k, v)
			}
			req.RemoteAddr = tt.remote
			got := ClientIP(req)
			if got != tt.expected {
				t.Errorf("ClientIP() = %q, want %q", got, tt.expected)
			}
		})
	}
}

func TestTokensMatch(t *testing.T) {
	tests := []struct {
		a, b   string
		expect bool
	}{
		{"abc", "abc", true},
		{"abc", "abd", false},
		{"abc", "ab", false},
		{"", "", false},
		{"abc", "ABC", false},
	}
	for _, tt := range tests {
		if got := TokensMatch(tt.a, tt.b); got != tt.expect {
			t.Errorf("TokensMatch(%q,%q) = %v, want %v", tt.a, tt.b, got, tt.expect)
		}
	}
}

func TestOriginAllowed(t *testing.T) {
	config.Cfg = config.Config{AllowedOrigins: []string{"http://localhost:3001", "http://localhost:3000"}}
	tests := []struct {
		origin string
		expect bool
	}{
		{"http://localhost:3001", true},
		{"http://localhost:3000", true},
		{"http://evil.com", false},
		{"", false},
	}
	for _, tt := range tests {
		if got := OriginAllowed(tt.origin); got != tt.expect {
			t.Errorf("OriginAllowed(%q) = %v, want %v", tt.origin, got, tt.expect)
		}
	}
}

func TestJWTSignVerify(t *testing.T) {
	config.Cfg = config.Config{CustomerJWTSecret: "test-secret-key-for-jwt-signing-32chars"}
	claims := map[string]interface{}{
		"sub":   "user-123",
		"email": "test@test.com",
		"name":  "Test User",
	}
	token, exp, err := SignJWT(claims, config.Cfg.CustomerJWTSecret, 1*time.Hour)
	if err != nil {
		t.Fatalf("SignJWT error: %v", err)
	}
	if token == "" {
		t.Fatal("SignJWT returned empty token")
	}
	if exp.Before(time.Now()) {
		t.Fatal("SignJWT expiration is in the past")
	}

	// Verify
	verified, err := VerifyJWT(token, config.Cfg.CustomerJWTSecret)
	if err != nil {
		t.Fatalf("VerifyJWT error: %v", err)
	}
	if verified["sub"] != "user-123" {
		t.Errorf("sub = %v, want user-123", verified["sub"])
	}
	if verified["email"] != "test@test.com" {
		t.Errorf("email = %v, want test@test.com", verified["email"])
	}
}

func TestJWTVerifyInvalid(t *testing.T) {
	config.Cfg = config.Config{CustomerJWTSecret: "test-secret-key-for-jwt-signing-32chars"}
	// Wrong secret
	claims := map[string]interface{}{"sub": "user-123", "email": "test@test.com", "name": "Test"}
	token, _, _ := SignJWT(claims, config.Cfg.CustomerJWTSecret, 1*time.Hour)
	_, err := VerifyJWT(token, "wrong-secret-key-here-32-chars-long!!")
	if err == nil {
		t.Error("VerifyJWT with wrong secret should fail")
	}
}

func TestSetCookieDevelopment(t *testing.T) {
	config.Cfg = config.Config{NodeEnv: "development"}
	w := httptest.NewRecorder()
	SetCookie(w, "test", "value", time.Now().Add(time.Hour))
	cookies := w.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("expected 1 cookie, got %d", len(cookies))
	}
	c := cookies[0]
	if c.Secure {
		t.Error("cookie should not be Secure in development")
	}
	if c.SameSite != http.SameSiteLaxMode {
		t.Error("cookie SameSite should be Lax in development")
	}
	if !c.HttpOnly {
		t.Error("cookie should be HttpOnly")
	}
	if c.Path != "/" {
		t.Errorf("cookie Path = %q, want /", c.Path)
	}
}

func TestSetCookieProduction(t *testing.T) {
	config.Cfg = config.Config{NodeEnv: "production"}
	w := httptest.NewRecorder()
	SetCookie(w, "test", "value", time.Now().Add(time.Hour))
	cookies := w.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("expected 1 cookie, got %d", len(cookies))
	}
	c := cookies[0]
	if !c.Secure {
		t.Error("cookie should be Secure in production")
	}
	if c.SameSite != http.SameSiteNoneMode {
		t.Error("cookie SameSite should be None in production")
	}
}

func TestCSRFProtection(t *testing.T) {
	config.Cfg = config.Config{
		AllowedOrigins:    []string{"http://localhost:3001"},
		CustomerJWTSecret: "test-secret-32-characters-long!!!",
	}

	// Test: mutating request without X-Requested-With header should be blocked
	req, _ := http.NewRequest("POST", "/api/test", nil)
	req.Header.Set("Origin", "http://localhost:3001")
	w := httptest.NewRecorder()

	handler := CSRF(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("CSRF should block POST without X-Requested-With, got %d", w.Code)
	}
}

func TestCSRFBlocksWithoutOrigin(t *testing.T) {
	config.Cfg = config.Config{AllowedOrigins: []string{"http://localhost:3001"}}

	req, _ := http.NewRequest("POST", "/api/test", nil)
	req.Header.Set("X-Requested-With", "cutmax")
	// No Origin header
	w := httptest.NewRecorder()

	handler := CSRF(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))
	handler.ServeHTTP(w, req)

	// Without origin, CSRF should block the request
	if w.Code != http.StatusForbidden {
		t.Errorf("CSRF should block POST without Origin, got %d", w.Code)
	}
}

func TestRateLimiterBuckets(t *testing.T) {
	buckets := []RLBucket{
		RLAdminLogin, RLCustomerLogin, RLCustomerReg,
		RLEnquirySubmit, RLSubscribe, RLBulkProducts,
		RLBulkImages, RLImageUpload,
	}
	seen := make(map[RLBucket]bool)
	for _, b := range buckets {
		if seen[b] {
			t.Errorf("duplicate rate limit bucket: %s", b)
		}
		seen[b] = true
		if b == "" {
			t.Error("empty rate limit bucket name")
		}
	}
}
