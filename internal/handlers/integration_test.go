package handlers_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/cutmax/cutmax-backend/internal/config"
	"github.com/cutmax/cutmax-backend/internal/db"
	"github.com/cutmax/cutmax-backend/internal/router"
	"github.com/cutmax/cutmax-backend/internal/storage"
)

// ===== Integration test setup =====

func setupTestConfig(t *testing.T) {
	t.Helper()
	config.Cfg = config.Config{
		DatabaseURL:          envOrDefault("TEST_DATABASE_URL", "postgresql://cutmax:cutmax_dev_password@localhost:5432/cutmax"),
		RedisURL:             envOrDefault("TEST_REDIS_URL", "redis://localhost:6379"),
		CustomerJWTSecret:    "test-customer-secret-minimum-32-characters",
		AdminJWTSecret:       "test-admin-secret-minimum-32-characters-long",
		AllowedOrigins:       []string{"http://localhost:3001", "http://localhost:3000"},
		UploadsDir:           "./test-uploads",
		UploadsPublicBaseURL: "http://localhost:3000/api/uploads",
		MaxUploadMB:          5,
		StorageDriver:        "local",
		EmailProvider:        "smtp",
		SMTPHost:             "localhost",
		SMTPPort:             1025,
		SMTPFrom:             "CutMax <no-reply@cutmax.local>",
		NodeEnv:              "development",
		Port:                 3000,
	}
	active, err := storage.New()
	if err != nil {
		t.Fatalf("storage.New: %v", err)
	}
	storage.Active = active
}

func envOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func buildTestRouter(pool *pgxpool.Pool) http.Handler {
	db.Pool = pool
	return router.New()
}

func setupIntegrationTest(t *testing.T) (*httptest.Server, *pgxpool.Pool) {
	t.Helper()
	setupTestConfig(t)
	pool := setupTestDB(t)
	t.Cleanup(func() { pool.Close() })
	// Flush rate limiter keys to avoid cross-test interference
	flushRateLimitKeys(t)
	r := buildTestRouter(pool)
	ts := httptest.NewServer(r)
	t.Cleanup(ts.Close)
	return ts, pool
}

func flushRateLimitKeys(t *testing.T) {
	t.Helper()
	rdb := setupTestRedis(t)
	defer rdb.Close()
	ctx := t.Context()
	keys, err := rdb.Keys(ctx, "rl:*").Result()
	if err != nil {
		return
	}
	if len(keys) > 0 {
		rdb.Del(ctx, keys...)
	}
}

// ===== Helper to make requests =====

type testRequest struct {
	method  string
	path    string
	body    interface{}
	headers map[string]string
	cookies []*http.Cookie
}

func doRequest(t *testing.T, ts *httptest.Server, tr testRequest) *httptest.ResponseRecorder {
	t.Helper()
	var body *bytes.Buffer
	if tr.body != nil {
		b, _ := json.Marshal(tr.body)
		body = bytes.NewBuffer(b)
	} else {
		body = bytes.NewBuffer(nil)
	}
	req, _ := http.NewRequest(tr.method, ts.URL+tr.path, body)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Requested-With", "cutmax")
	req.Header.Set("Origin", "http://localhost:3001")
	for k, v := range tr.headers {
		req.Header.Set(k, v)
	}
	for _, c := range tr.cookies {
		req.AddCookie(c)
	}
	w := httptest.NewRecorder()
	ts.Config.Handler.ServeHTTP(w, req)
	return w
}

func getCookie(resp *http.Response, name string) *http.Cookie {
	for _, c := range resp.Cookies() {
		if c.Name == name {
			return c
		}
	}
	return nil
}

func parseJSON(t *testing.T, w *httptest.ResponseRecorder) map[string]interface{} {
	t.Helper()
	var result map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&result); err != nil {
		t.Fatalf("failed to parse JSON response: %v", err)
	}
	return result
}

// ===== Health Endpoint Tests =====

func TestHealthEndpoint(t *testing.T) {
	ts, _ := setupIntegrationTest(t)
	w := doRequest(t, ts, testRequest{method: "GET", path: "/api/health"})
	if w.Code != 200 {
		t.Errorf("health status = %d, want 200", w.Code)
	}
	resp := parseJSON(t, w)
	if resp["status"] != "ok" {
		t.Errorf("health status = %v, want ok", resp["status"])
	}
}

// ===== Public Product Tests =====

func TestListProducts(t *testing.T) {
	ts, _ := setupIntegrationTest(t)
	w := doRequest(t, ts, testRequest{method: "GET", path: "/api/public/products"})
	if w.Code != 200 {
		t.Errorf("list products status = %d, want 200", w.Code)
	}
	resp := parseJSON(t, w)
	if resp["success"] != true {
		t.Error("list products should return success=true")
	}
	if _, ok := resp["products"]; !ok {
		t.Error("response should contain products key")
	}
	if _, ok := resp["settings"]; !ok {
		t.Error("response should contain settings key")
	}
}

func TestListProductsWithCategoryFilter(t *testing.T) {
	ts, _ := setupIntegrationTest(t)
	w := doRequest(t, ts, testRequest{method: "GET", path: "/api/public/products?category=Inserts"})
	if w.Code != 200 {
		t.Errorf("list products with filter status = %d, want 200", w.Code)
	}
}

func TestGetProductNotFound(t *testing.T) {
	ts, _ := setupIntegrationTest(t)
	w := doRequest(t, ts, testRequest{method: "GET", path: "/api/public/products/NONEXISTENT-SKU"})
	if w.Code != 404 {
		t.Errorf("get product not found status = %d, want 404", w.Code)
	}
}

func TestGetProductFound(t *testing.T) {
	ts, pool := setupIntegrationTest(t)
	// Insert a test product
	sku := fmt.Sprintf("TEST-%d", time.Now().UnixNano())
	_, err := pool.Exec(t.Context(),
		`INSERT INTO products (id,sku,name,category,sub_category,brand,description,price,stock,unit,active,created_at,updated_at)
		 VALUES ($1,$2,'Test Product','Inserts','CNMG','TestBrand','A test product',100.00,100,'NOS',true,NOW(),NOW())`,
		"prod-test-"+fmt.Sprintf("%d", time.Now().UnixNano()), sku,
	)
	if err != nil {
		t.Fatalf("insert test product: %v", err)
	}

	w := doRequest(t, ts, testRequest{method: "GET", path: "/api/public/products/" + sku})
	if w.Code != 200 {
		t.Errorf("get product status = %d, want 200", w.Code)
	}
	resp := parseJSON(t, w)
	if resp["success"] != true {
		t.Error("get product should return success=true")
	}
}

func TestPriceTiers(t *testing.T) {
	ts, _ := setupIntegrationTest(t)
	w := doRequest(t, ts, testRequest{method: "GET", path: "/api/public/price-tiers"})
	if w.Code != 200 {
		t.Errorf("price tiers status = %d, want 200", w.Code)
	}
}

// ===== Customer Auth Tests =====

func TestRegisterSuccess(t *testing.T) {
	ts, _ := setupIntegrationTest(t)
	email := fmt.Sprintf("reg_%d@test.com", time.Now().UnixNano())
	w := doRequest(t, ts, testRequest{
		method: "POST",
		path:   "/api/public/auth/register",
		body:   map[string]string{"name": "Reg User", "email": email, "phone": "1234567890", "password": "password1234"},
	})
	if w.Code != 201 {
		t.Errorf("register status = %d, want 201", w.Code)
	}
	resp := parseJSON(t, w)
	if resp["success"] != true {
		t.Error("register should return success=true")
	}
	// Should set cookie
	cookie := getCookie(w.Result(), "cutmax_customer")
	if cookie == nil {
		t.Error("register should set cutmax_customer cookie")
	}
}

func TestRegisterDuplicateEmail(t *testing.T) {
	ts, _ := setupIntegrationTest(t)
	email := fmt.Sprintf("dup_%d@test.com", time.Now().UnixNano())
	// First registration
	doRequest(t, ts, testRequest{
		method: "POST",
		path:   "/api/public/auth/register",
		body:   map[string]string{"name": "User1", "email": email, "phone": "1111111111", "password": "password1234"},
	})
	// Second registration with same email
	w := doRequest(t, ts, testRequest{
		method: "POST",
		path:   "/api/public/auth/register",
		body:   map[string]string{"name": "User2", "email": email, "phone": "2222222222", "password": "password1234"},
	})
	if w.Code != 409 {
		t.Errorf("duplicate register status = %d, want 409", w.Code)
	}
}

func TestRegisterShortPassword(t *testing.T) {
	ts, _ := setupIntegrationTest(t)
	w := doRequest(t, ts, testRequest{
		method: "POST",
		path:   "/api/public/auth/register",
		body:   map[string]string{"name": "User", "email": "short@test.com", "phone": "1234567890", "password": "short"},
	})
	if w.Code != 422 {
		t.Errorf("short password register status = %d, want 422", w.Code)
	}
}

func TestRegisterMissingFields(t *testing.T) {
	ts, _ := setupIntegrationTest(t)
	w := doRequest(t, ts, testRequest{
		method: "POST",
		path:   "/api/public/auth/register",
		body:   map[string]string{"name": "User"},
	})
	if w.Code != 400 {
		t.Errorf("missing fields register status = %d, want 400", w.Code)
	}
}

func TestLoginSuccess(t *testing.T) {
	ts, pool := setupIntegrationTest(t)
	email, password, _ := seedCustomer(t, pool)
	w := doRequest(t, ts, testRequest{
		method: "POST",
		path:   "/api/public/auth/login",
		body:   map[string]string{"email": email, "password": password},
	})
	if w.Code != 200 {
		t.Errorf("login status = %d, want 200", w.Code)
	}
	resp := parseJSON(t, w)
	if resp["success"] != true {
		t.Error("login should return success=true")
	}
	cookie := getCookie(w.Result(), "cutmax_customer")
	if cookie == nil {
		t.Error("login should set cutmax_customer cookie")
	}
}

func TestLoginWrongPassword(t *testing.T) {
	ts, pool := setupIntegrationTest(t)
	email, _, _ := seedCustomer(t, pool)
	w := doRequest(t, ts, testRequest{
		method: "POST",
		path:   "/api/public/auth/login",
		body:   map[string]string{"email": email, "password": "wrongpassword"},
	})
	if w.Code != 401 {
		t.Errorf("wrong password login status = %d, want 401", w.Code)
	}
}

func TestLoginNonexistentEmail(t *testing.T) {
	ts, _ := setupIntegrationTest(t)
	w := doRequest(t, ts, testRequest{
		method: "POST",
		path:   "/api/public/auth/login",
		body:   map[string]string{"email": "nonexistent@test.com", "password": "password1234"},
	})
	if w.Code != 401 {
		t.Errorf("nonexistent email login status = %d, want 401", w.Code)
	}
}

func TestMeAuthenticated(t *testing.T) {
	ts, pool := setupIntegrationTest(t)
	email, password, custID := seedCustomer(t, pool)
	token := loginCustomer(t, ts.URL, email, password)
	w := doRequest(t, ts, testRequest{
		method:  "GET",
		path:    "/api/public/auth/me",
		cookies: []*http.Cookie{{Name: "cutmax_customer", Value: token}},
	})
	if w.Code != 200 {
		t.Errorf("me status = %d, want 200", w.Code)
	}
	resp := parseJSON(t, w)
	if resp["authenticated"] != true {
		t.Error("me should return authenticated=true")
	}
	cust := resp["customer"].(map[string]interface{})
	if cust["id"] != custID {
		t.Errorf("me customer id = %v, want %v", cust["id"], custID)
	}
}

func TestMeUnauthenticated(t *testing.T) {
	ts, _ := setupIntegrationTest(t)
	w := doRequest(t, ts, testRequest{method: "GET", path: "/api/public/auth/me"})
	if w.Code != 200 {
		t.Errorf("me unauth status = %d, want 200", w.Code)
	}
	resp := parseJSON(t, w)
	if resp["authenticated"] != false {
		t.Error("me unauth should return authenticated=false")
	}
}

func TestLogout(t *testing.T) {
	ts, pool := setupIntegrationTest(t)
	email, password, _ := seedCustomer(t, pool)
	token := loginCustomer(t, ts.URL, email, password)
	w := doRequest(t, ts, testRequest{
		method:  "POST",
		path:    "/api/public/auth/logout",
		cookies: []*http.Cookie{{Name: "cutmax_customer", Value: token}},
	})
	if w.Code != 200 {
		t.Errorf("logout status = %d, want 200", w.Code)
	}
	// Cookie should be cleared
	cookie := getCookie(w.Result(), "cutmax_customer")
	if cookie != nil && cookie.Value != "" {
		t.Error("logout should clear the cookie")
	}
}

// ===== Enquiry Tests =====

func TestCreateEnquiry(t *testing.T) {
	ts, pool := setupIntegrationTest(t)
	email, password, _ := seedCustomer(t, pool)
	token := loginCustomer(t, ts.URL, email, password)
	w := doRequest(t, ts, testRequest{
		method:  "POST",
		path:    "/api/public/enquiries",
		cookies: []*http.Cookie{{Name: "cutmax_customer", Value: token}},
		body: map[string]interface{}{
			"name":     "Test Customer",
			"phone":    "1234567890",
			"items":    []map[string]interface{}{{"SKU": "CM-001", "Name": "Insert", "Category": "Inserts", "Qty": 10, "UnitPrice": 100, "LineTotal": 1000}},
			"subtotal": 1000, "gstRate": 18, "gstAmount": 180, "grandTotal": 1180,
		},
	})
	if w.Code != 201 {
		t.Errorf("create enquiry status = %d, want 201", w.Code)
	}
	resp := parseJSON(t, w)
	if resp["success"] != true {
		t.Error("create enquiry should return success=true")
	}
	if resp["reference"] == nil || resp["reference"] == "" {
		t.Error("create enquiry should return reference")
	}
}

func TestCreateEnquiryLinksToCustomer(t *testing.T) {
	ts, pool := setupIntegrationTest(t)
	email, password, custID := seedCustomer(t, pool)
	token := loginCustomer(t, ts.URL, email, password)

	// Submit enquiry
	w := doRequest(t, ts, testRequest{
		method:  "POST",
		path:    "/api/public/enquiries",
		cookies: []*http.Cookie{{Name: "cutmax_customer", Value: token}},
		body: map[string]interface{}{
			"name":     "Test Customer",
			"phone":    "1234567890",
			"items":    []map[string]interface{}{{"SKU": "CM-001", "Name": "Insert", "Category": "Inserts", "Qty": 10, "UnitPrice": 100, "LineTotal": 1000}},
			"subtotal": 1000, "gstRate": 18, "gstAmount": 180, "grandTotal": 1180,
		},
	})
	if w.Code != 201 {
		t.Fatalf("create enquiry status = %d, want 201", w.Code)
	}

	// Check my-enquiries
	w2 := doRequest(t, ts, testRequest{
		method:  "GET",
		path:    "/api/public/auth/my-enquiries",
		cookies: []*http.Cookie{{Name: "cutmax_customer", Value: token}},
	})
	if w2.Code != 200 {
		t.Errorf("my-enquiries status = %d, want 200", w2.Code)
	}
	resp := parseJSON(t, w2)
	enqs := resp["enquiries"].([]interface{})
	if len(enqs) == 0 {
		t.Fatal("my-enquiries should return at least one enquiry")
	}

	// Verify the enquiry has customer_id set
	var customerID string
	err := pool.QueryRow(t.Context(),
		"SELECT customer_id FROM enquiries WHERE customer_id=$1 LIMIT 1", custID,
	).Scan(&customerID)
	if err != nil {
		t.Errorf("enquiry not linked to customer: %v", err)
	}
}

func TestCreateEnquiryMissingFields(t *testing.T) {
	ts, _ := setupIntegrationTest(t)
	w := doRequest(t, ts, testRequest{
		method: "POST",
		path:   "/api/public/enquiries",
		body:   map[string]interface{}{"name": "Test"},
	})
	if w.Code != 422 {
		t.Errorf("missing fields enquiry status = %d, want 422", w.Code)
	}
}

func TestCreateEnquiryNoItems(t *testing.T) {
	ts, _ := setupIntegrationTest(t)
	w := doRequest(t, ts, testRequest{
		method: "POST",
		path:   "/api/public/enquiries",
		body: map[string]interface{}{
			"name":  "Test Customer",
			"phone": "1234567890",
			"items": []map[string]interface{}{},
		},
	})
	if w.Code != 422 {
		t.Errorf("no items enquiry status = %d, want 422", w.Code)
	}
}

func TestMyEnquiriesRequiresAuth(t *testing.T) {
	ts, _ := setupIntegrationTest(t)
	w := doRequest(t, ts, testRequest{method: "GET", path: "/api/public/auth/my-enquiries"})
	if w.Code != 401 {
		t.Errorf("my-enquiries without auth status = %d, want 401", w.Code)
	}
}

func TestMyEnquiriesReturnsLinkedEnquiries(t *testing.T) {
	ts, pool := setupIntegrationTest(t)
	email, password, _ := seedCustomer(t, pool)
	token := loginCustomer(t, ts.URL, email, password)

	// Submit an enquiry
	doRequest(t, ts, testRequest{
		method:  "POST",
		path:    "/api/public/enquiries",
		cookies: []*http.Cookie{{Name: "cutmax_customer", Value: token}},
		body: map[string]interface{}{
			"name":     "Test Customer",
			"phone":    "1234567890",
			"items":    []map[string]interface{}{{"SKU": "SKU-1", "Name": "Item1", "Category": "Inserts", "Qty": 5, "UnitPrice": 50, "LineTotal": 250}},
			"subtotal": 250, "gstRate": 18, "gstAmount": 45, "grandTotal": 295,
		},
	})

	// Get my-enquiries
	w := doRequest(t, ts, testRequest{
		method:  "GET",
		path:    "/api/public/auth/my-enquiries",
		cookies: []*http.Cookie{{Name: "cutmax_customer", Value: token}},
	})
	resp := parseJSON(t, w)
	enqs := resp["enquiries"].([]interface{})
	if len(enqs) != 1 {
		t.Fatalf("expected 1 enquiry, got %d", len(enqs))
	}
	enq := enqs[0].(map[string]interface{})
	if enq["reference"] == nil || enq["reference"] == "" {
		t.Error("enquiry should have a reference")
	}
}

// ===== Admin Auth Tests =====

func TestAdminLoginSuccess(t *testing.T) {
	ts, pool := setupIntegrationTest(t)
	email, password := seedAdmin(t, pool)
	w := doRequest(t, ts, testRequest{
		method: "POST",
		path:   "/api/admin/auth/login",
		body:   map[string]string{"email": email, "password": password},
	})
	if w.Code != 200 {
		t.Errorf("admin login status = %d, want 200", w.Code)
	}
	resp := parseJSON(t, w)
	if resp["success"] != true {
		t.Error("admin login should return success=true")
	}
	cookie := getCookie(w.Result(), "cutmax_admin")
	if cookie == nil {
		t.Error("admin login should set cutmax_admin cookie")
	}
}

func TestAdminLoginWrongPassword(t *testing.T) {
	ts, pool := setupIntegrationTest(t)
	email, _ := seedAdmin(t, pool)
	w := doRequest(t, ts, testRequest{
		method: "POST",
		path:   "/api/admin/auth/login",
		body:   map[string]string{"email": email, "password": "wrongpassword"},
	})
	if w.Code != 401 {
		t.Errorf("admin wrong password status = %d, want 401", w.Code)
	}
}

func TestAdminMeAuthenticated(t *testing.T) {
	ts, pool := setupIntegrationTest(t)
	email, password := seedAdmin(t, pool)
	token := loginAdmin(t, ts.URL, email, password)
	w := doRequest(t, ts, testRequest{
		method:  "GET",
		path:    "/api/admin/auth/me",
		cookies: []*http.Cookie{{Name: "cutmax_admin", Value: token}},
	})
	if w.Code != 200 {
		t.Errorf("admin me status = %d, want 200", w.Code)
	}
	resp := parseJSON(t, w)
	admin := resp["admin"].(map[string]interface{})
	if admin["email"] != email {
		t.Errorf("admin me email = %v, want %v", admin["email"], email)
	}
}

func TestAdminMeUnauthenticated(t *testing.T) {
	ts, _ := setupIntegrationTest(t)
	w := doRequest(t, ts, testRequest{method: "GET", path: "/api/admin/auth/me"})
	if w.Code != 401 {
		t.Errorf("admin me unauth status = %d, want 401", w.Code)
	}
}

func TestAdminLogout(t *testing.T) {
	ts, pool := setupIntegrationTest(t)
	email, password := seedAdmin(t, pool)
	token := loginAdmin(t, ts.URL, email, password)
	w := doRequest(t, ts, testRequest{
		method:  "POST",
		path:    "/api/admin/auth/logout",
		cookies: []*http.Cookie{{Name: "cutmax_admin", Value: token}},
	})
	if w.Code != 200 {
		t.Errorf("admin logout status = %d, want 200", w.Code)
	}
}

// ===== Admin Product CRUD Tests =====

func TestAdminListProducts(t *testing.T) {
	ts, pool := setupIntegrationTest(t)
	email, password := seedAdmin(t, pool)
	token := loginAdmin(t, ts.URL, email, password)
	w := doRequest(t, ts, testRequest{
		method:  "GET",
		path:    "/api/admin/products",
		cookies: []*http.Cookie{{Name: "cutmax_admin", Value: token}},
	})
	if w.Code != 200 {
		t.Errorf("admin list products status = %d, want 200", w.Code)
	}
}

func TestAdminCreateProduct(t *testing.T) {
	ts, pool := setupIntegrationTest(t)
	email, password := seedAdmin(t, pool)
	token := loginAdmin(t, ts.URL, email, password)
	sku := fmt.Sprintf("ADM-TEST-%d", time.Now().UnixNano())
	w := doRequest(t, ts, testRequest{
		method:  "POST",
		path:    "/api/admin/products",
		cookies: []*http.Cookie{{Name: "cutmax_admin", Value: token}},
		body: map[string]interface{}{
			"sku": sku, "name": "Admin Created", "category": "Inserts",
			"subCategory": "CNMG", "brand": "TestBrand", "price": 99.99, "stock": 50,
			"description": "Created via admin API", "unit": "NOS",
		},
	})
	if w.Code != 201 {
		t.Errorf("admin create product status = %d, body = %s, want 201", w.Code, w.Body.String())
	}
}

func TestAdminCreateProductUnauthorized(t *testing.T) {
	ts, _ := setupIntegrationTest(t)
	w := doRequest(t, ts, testRequest{
		method: "POST",
		path:   "/api/admin/products",
		body: map[string]interface{}{
			"sku": "UNAUTH-001", "name": "Unauthorized", "category": "Inserts",
			"subCategory": "CNMG", "brand": "Test", "price": 50, "stock": 10,
		},
	})
	if w.Code != 401 {
		t.Errorf("admin create product unauthorized status = %d, want 401", w.Code)
	}
}

func TestAdminUpdateProduct(t *testing.T) {
	ts, pool := setupIntegrationTest(t)
	email, password := seedAdmin(t, pool)
	token := loginAdmin(t, ts.URL, email, password)

	// Create a product first
	sku := fmt.Sprintf("UPD-%d", time.Now().UnixNano())
	pid := fmt.Sprintf("prod-%d", time.Now().UnixNano())
	_, err := pool.Exec(t.Context(),
		`INSERT INTO products (id,sku,name,category,sub_category,brand,description,price,stock,unit,active,created_at,updated_at)
		 VALUES ($1,$2,'Original','Inserts','CNMG','Brand','Test description',100,10,'NOS',true,NOW(),NOW())`,
		pid, sku,
	)
	if err != nil {
		t.Fatalf("insert product: %v", err)
	}

	w := doRequest(t, ts, testRequest{
		method:  "PUT",
		path:    "/api/admin/products/" + pid,
		cookies: []*http.Cookie{{Name: "cutmax_admin", Value: token}},
		body: map[string]interface{}{
			"sku": sku, "name": "Updated Name", "category": "Inserts",
			"subCategory": "CNMG", "brand": "Brand", "description": "Updated description", "price": 150, "stock": 20,
		},
	})
	if w.Code != 200 {
		t.Errorf("admin update product status = %d, want 200", w.Code)
	}
}

func TestAdminDeleteProduct(t *testing.T) {
	ts, pool := setupIntegrationTest(t)
	email, password := seedAdmin(t, pool)
	token := loginAdmin(t, ts.URL, email, password)

	pid := fmt.Sprintf("del-prod-%d", time.Now().UnixNano())
	sku := fmt.Sprintf("DEL-%d", time.Now().UnixNano())
	_, err := pool.Exec(t.Context(),
		`INSERT INTO products (id,sku,name,category,sub_category,brand,description,price,stock,unit,active,created_at,updated_at)
		 VALUES ($1,$2,'ToDelete','Inserts','CNMG','Brand','To be deleted',100,10,'NOS',true,NOW(),NOW())`,
		pid, sku,
	)
	if err != nil {
		t.Fatalf("insert product: %v", err)
	}

	w := doRequest(t, ts, testRequest{
		method:  "DELETE",
		path:    "/api/admin/products/" + pid,
		cookies: []*http.Cookie{{Name: "cutmax_admin", Value: token}},
	})
	if w.Code != 200 {
		t.Errorf("admin delete product status = %d, want 200", w.Code)
	}
}

// ===== Admin Settings Tests =====

func TestAdminGetSettings(t *testing.T) {
	ts, pool := setupIntegrationTest(t)
	email, password := seedAdmin(t, pool)
	token := loginAdmin(t, ts.URL, email, password)
	w := doRequest(t, ts, testRequest{
		method:  "GET",
		path:    "/api/admin/settings",
		cookies: []*http.Cookie{{Name: "cutmax_admin", Value: token}},
	})
	if w.Code != 200 {
		t.Errorf("admin get settings status = %d, want 200", w.Code)
	}
}

func TestAdminUpdateSettings(t *testing.T) {
	ts, pool := setupIntegrationTest(t)
	email, password := seedAdmin(t, pool)
	token := loginAdmin(t, ts.URL, email, password)
	w := doRequest(t, ts, testRequest{
		method:  "PUT",
		path:    "/api/admin/settings",
		cookies: []*http.Cookie{{Name: "cutmax_admin", Value: token}},
		body: map[string]interface{}{
			"company_name": "CutMax Technologies",
			"low_stock":    5,
			"gst_rate":     18,
		},
	})
	if w.Code != 200 {
		t.Errorf("admin update settings status = %d, want 200", w.Code)
	}
}

// ===== Admin Stats Tests =====

func TestAdminStats(t *testing.T) {
	ts, pool := setupIntegrationTest(t)
	email, password := seedAdmin(t, pool)
	token := loginAdmin(t, ts.URL, email, password)
	w := doRequest(t, ts, testRequest{
		method:  "GET",
		path:    "/api/admin/stats",
		cookies: []*http.Cookie{{Name: "cutmax_admin", Value: token}},
	})
	if w.Code != 200 {
		t.Errorf("admin stats status = %d, want 200", w.Code)
	}
	resp := parseJSON(t, w)
	if _, ok := resp["kpis"]; !ok {
		t.Error("stats should contain kpis")
	}
}

// ===== Admin Enquiries Tests =====

func TestAdminListEnquiries(t *testing.T) {
	ts, pool := setupIntegrationTest(t)
	email, password := seedAdmin(t, pool)
	token := loginAdmin(t, ts.URL, email, password)
	w := doRequest(t, ts, testRequest{
		method:  "GET",
		path:    "/api/admin/enquiries",
		cookies: []*http.Cookie{{Name: "cutmax_admin", Value: token}},
	})
	if w.Code != 200 {
		t.Errorf("admin list enquiries status = %d, want 200", w.Code)
	}
}

// ===== Admin Audit Log Tests =====

func TestAdminAuditLog(t *testing.T) {
	ts, pool := setupIntegrationTest(t)
	email, password := seedAdmin(t, pool)
	token := loginAdmin(t, ts.URL, email, password)
	w := doRequest(t, ts, testRequest{
		method:  "GET",
		path:    "/api/admin/audit",
		cookies: []*http.Cookie{{Name: "cutmax_admin", Value: token}},
	})
	if w.Code != 200 {
		t.Errorf("admin audit status = %d, want 200", w.Code)
	}
}

// ===== Subscribe Tests =====

func TestSubscribe(t *testing.T) {
	ts, _ := setupIntegrationTest(t)
	email := fmt.Sprintf("sub_%d@test.com", time.Now().UnixNano())
	w := doRequest(t, ts, testRequest{
		method: "POST",
		path:   "/api/public/subscribe",
		body:   map[string]string{"email": email},
	})
	if w.Code != 200 {
		t.Errorf("subscribe status = %d, want 200", w.Code)
	}
}

func TestSubscribeDuplicate(t *testing.T) {
	ts, _ := setupIntegrationTest(t)
	email := fmt.Sprintf("subdup_%d@test.com", time.Now().UnixNano())
	doRequest(t, ts, testRequest{
		method: "POST",
		path:   "/api/public/subscribe",
		body:   map[string]string{"email": email},
	})
	// Second subscribe should still succeed (ON CONFLICT DO NOTHING)
	w := doRequest(t, ts, testRequest{
		method: "POST",
		path:   "/api/public/subscribe",
		body:   map[string]string{"email": email},
	})
	if w.Code != 200 {
		t.Errorf("duplicate subscribe status = %d, want 200", w.Code)
	}
}

func TestSubscribeInvalidEmail(t *testing.T) {
	ts, _ := setupIntegrationTest(t)
	w := doRequest(t, ts, testRequest{
		method: "POST",
		path:   "/api/public/subscribe",
		body:   map[string]string{"email": ""},
	})
	if w.Code != 400 {
		t.Errorf("invalid subscribe status = %d, want 400", w.Code)
	}
}

// ===== CORS Tests =====

func TestCORSHeaders(t *testing.T) {
	ts, _ := setupIntegrationTest(t)
	w := doRequest(t, ts, testRequest{method: "GET", path: "/api/health"})
	if w.Header().Get("Access-Control-Allow-Origin") == "" {
		t.Error("CORS header should be set")
	}
}

// ===== Security Headers Tests =====

func TestSecurityHeaders(t *testing.T) {
	ts, _ := setupIntegrationTest(t)
	w := doRequest(t, ts, testRequest{method: "GET", path: "/api/health"})
	// Check security headers are present
	if w.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Error("X-Content-Type-Options should be nosniff")
	}
	if w.Header().Get("X-Frame-Options") != "DENY" {
		t.Error("X-Frame-Options should be DENY")
	}
	if w.Header().Get("X-XSS-Protection") != "1; mode=block" {
		t.Error("X-XSS-Protection should be set")
	}
}

// ===== CSRF Tests =====

func TestCSRFTokenEndpoint(t *testing.T) {
	ts, _ := setupIntegrationTest(t)
	w := doRequest(t, ts, testRequest{method: "GET", path: "/api/public/csrf"})
	if w.Code != 200 {
		t.Errorf("csrf endpoint status = %d, want 200", w.Code)
	}
	resp := parseJSON(t, w)
	if resp["csrfToken"] == nil || resp["csrfToken"] == "" {
		t.Error("csrf endpoint should return csrfToken")
	}
}

// ===== Account Lockout Tests =====

func TestAccountLockout(t *testing.T) {
	ts, pool := setupIntegrationTest(t)
	email, _, _ := seedCustomer(t, pool)

	// Attempt 5 wrong passwords
	for i := 0; i < 5; i++ {
		doRequest(t, ts, testRequest{
			method: "POST",
			path:   "/api/public/auth/login",
			body:   map[string]string{"email": email, "password": "wrongpassword"},
		})
	}

	// 6th attempt should be locked
	w := doRequest(t, ts, testRequest{
		method: "POST",
		path:   "/api/public/auth/login",
		body:   map[string]string{"email": email, "password": "wrongpassword"},
	})
	if w.Code != 423 {
		t.Errorf("locked account status = %d, want 423", w.Code)
	}
}
