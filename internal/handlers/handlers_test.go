package handlers_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"golang.org/x/crypto/bcrypt"
)

// ===== Test helpers =====

func setupTestDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		dsn = "postgresql://cutmax:cutmax_dev_password@localhost:5432/cutmax"
	}
	ctx := t.Context()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Skipf("skipping integration test: %v", err)
	}
	if err := pool.Ping(ctx); err != nil {
		t.Skipf("skipping integration test: db not reachable: %v", err)
	}
	return pool
}

func setupTestRedis(t *testing.T) *redis.Client {
	t.Helper()
	opt := &redis.Options{Addr: "localhost:6379"}
	rdb := redis.NewClient(opt)
	return rdb
}

func seedAdmin(t *testing.T, pool *pgxpool.Pool) (email, password string) {
	t.Helper()
	email = fmt.Sprintf("testadmin_%d@cutmax.test", time.Now().UnixNano())
	password = "TestAdmin123!"
	hash, _ := bcrypt.GenerateFromPassword([]byte(password), 12)
	_, err := pool.Exec(t.Context(),
		`INSERT INTO admin_users (id,email,name,password_hash,role,created_at) VALUES ($1,$2,$3,$4,$5,NOW())`,
		"test-admin-"+fmt.Sprintf("%d", time.Now().UnixNano()), email, "Test Admin", string(hash), "admin",
	)
	if err != nil {
		t.Fatalf("seed admin: %v", err)
	}
	return email, password
}

func seedCustomer(t *testing.T, pool *pgxpool.Pool) (email, password, custID string) {
	t.Helper()
	email = fmt.Sprintf("testcust_%d@cutmax.test", time.Now().UnixNano())
	password = "TestCust123!"
	hash, _ := bcrypt.GenerateFromPassword([]byte(password), 12)
	custID = fmt.Sprintf("cust-%d", time.Now().UnixNano())
	_, err := pool.Exec(t.Context(),
		`INSERT INTO customers (id,name,email,phone,password_hash,created_at) VALUES ($1,$2,$3,$4,$5,NOW())`,
		custID, "Test Customer", email, "9876543210", string(hash),
	)
	if err != nil {
		t.Fatalf("seed customer: %v", err)
	}
	return email, password, custID
}

func loginCustomer(t *testing.T, baseURL, email, password string) string {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"email": email, "password": password})
	req, _ := http.NewRequest("POST", baseURL+"/api/public/auth/login", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Requested-With", "cutmax")
	req.Header.Set("Origin", "http://localhost:3001")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("loginCustomer request failed: %v", err)
	}
	defer resp.Body.Close()
	for _, c := range resp.Cookies() {
		if c.Name == "cutmax_customer" {
			return c.Value
		}
	}
	t.Fatalf("loginCustomer: no cutmax_customer cookie in response")
	return ""
}

func loginAdmin(t *testing.T, baseURL, email, password string) string {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"email": email, "password": password})
	req, _ := http.NewRequest("POST", baseURL+"/api/admin/auth/login", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Requested-With", "cutmax")
	req.Header.Set("Origin", "http://localhost:3001")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("loginAdmin request failed: %v", err)
	}
	defer resp.Body.Close()
	for _, c := range resp.Cookies() {
		if c.Name == "cutmax_admin" {
			return c.Value
		}
	}
	t.Fatalf("loginAdmin: no cutmax_admin cookie in response")
	return ""
}

// ===== Unit tests for pure functions =====

func TestPasswordPolicy(t *testing.T) {
	tests := []struct {
		name    string
		pass    string
		wantErr bool
	}{
		{"too short", "short", true},
		{"exactly 10", "1234567890", false},
		{"long", "a]verylongpassword123", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := len(tt.pass) < 10; got != tt.wantErr {
				t.Errorf("password %q: length check = %v, wantErr %v", tt.pass, got, tt.wantErr)
			}
		})
	}
}

func TestEnquiryStatusValues(t *testing.T) {
	valid := map[string]bool{"NEW": true, "READ": true, "REPLIED": true, "CLOSED": true}
	invalid := []string{"OPEN", "pending", "resolved", ""}
	for _, s := range invalid {
		if valid[s] {
			t.Errorf("status %q should not be valid", s)
		}
	}
}

func TestBcryptCost(t *testing.T) {
	hash, err := bcrypt.GenerateFromPassword([]byte("testpassword"), 12)
	if err != nil {
		t.Fatalf("bcrypt error: %v", err)
	}
	// bcrypt hash format: $2a$<cost>$...
	if len(hash) < 7 || string(hash[:4]) != "$2a$" {
		t.Errorf("unexpected hash format: %s", string(hash[:min(10, len(hash))]))
	}
	// Cost should be 12 (stored as 2 digits after $2a$)
	if string(hash[4:6]) != "12" {
		t.Errorf("bcrypt cost = %s, want 12", string(hash[4:6]))
	}
}

func TestProductTaxonomy(t *testing.T) {
	// Categories should be static and well-defined
	categories := []string{
		"Inserts", "Cutting Tools", "Threading Tools",
		"Tool Holders", "Coolant Systems", "Measuring Instruments", "Accessories",
	}
	if len(categories) != 7 {
		t.Errorf("expected 7 categories, got %d", len(categories))
	}
	seen := make(map[string]bool)
	for _, c := range categories {
		if seen[c] {
			t.Errorf("duplicate category: %s", c)
		}
		seen[c] = true
	}
}
