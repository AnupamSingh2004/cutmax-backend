package handlers

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/cutmax/cutmax-backend/internal/config"
	"github.com/cutmax/cutmax-backend/internal/db"
	"github.com/cutmax/cutmax-backend/internal/middleware"
	"github.com/cutmax/cutmax-backend/internal/util"
)

// writeCached serves a value from cache, or marshals+caches+serves a fresh one.
func writeCached(w http.ResponseWriter, r *http.Request, key string, ttl time.Duration, build func() map[string]interface{}) {
	if cached, ok := middleware.CacheGet(r.Context(), key); ok {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(cached))
		return
	}
	data := build()
	data["success"] = true
	b, err := json.Marshal(data)
	if err != nil {
		util.JsonOK(w, 200, data)
		return
	}
	middleware.CacheSet(r.Context(), key, string(b), ttl)
	w.Header().Set("Content-Type", "application/json")
	w.Write(b)
}

// ===== CSRF Endpoint =====

func HandleCSRF(w http.ResponseWriter, r *http.Request) {
	b := make([]byte, 32)
	rand.Read(b)
	token := fmt.Sprintf("%x", b)
	secure := config.Cfg.NodeEnv == "production"
	sameSite := http.SameSiteLaxMode
	if secure {
		sameSite = http.SameSiteNoneMode
	}
	http.SetCookie(w, &http.Cookie{
		Name: middleware.CSRFCookie, Value: token, Path: "/", HttpOnly: false,
		Secure: secure, SameSite: sameSite, MaxAge: 4 * 3600,
	})
	util.JsonOK(w, 200, map[string]interface{}{"csrfToken": token})
}

// ===== Health =====

func HandleHealth(w http.ResponseWriter, r *http.Request) {
	if err := db.Pool.Ping(r.Context()); err != nil {
		util.JsonErr(w, 503, "Database unavailable")
		return
	}
	util.JsonOK(w, 200, map[string]interface{}{"status": "ok"})
}

// ===== Public Products =====

func HandleListProducts(w http.ResponseWriter, r *http.Request) {
	writeCached(w, r, "cache:products:list:"+r.URL.RawQuery, 60*time.Second, func() map[string]interface{} {
		return buildProductList(r)
	})
}

func buildProductList(r *http.Request) map[string]interface{} {
	q := r.URL.Query()
	where := "p.active = true"
	args := []interface{}{}
	idx := 1

	if v := q.Get("category"); v != "" {
		where += fmt.Sprintf(" AND p.category = $%d", idx)
		args = append(args, v)
		idx++
	}
	if v := q.Get("sub"); v != "" {
		where += fmt.Sprintf(" AND p.sub_category = $%d", idx)
		args = append(args, v)
		idx++
	}
	if v := q.Get("brand"); v != "" {
		where += fmt.Sprintf(" AND p.brand = $%d", idx)
		args = append(args, v)
		idx++
	}
	if v := q.Get("material"); v != "" {
		where += fmt.Sprintf(" AND p.material = $%d", idx)
		args = append(args, v)
		idx++
	}
	if v := q.Get("q"); v != "" {
		where += fmt.Sprintf(" AND (p.name ILIKE '%%'||$%d||'%%' OR p.sku ILIKE '%%'||$%d||'%%')", idx, idx)
		args = append(args, v)
		idx++
	}

	orderBy := "p.created_at DESC"
	switch q.Get("sort") {
	case "name":
		orderBy = "p.name ASC"
	case "name_desc":
		orderBy = "p.name DESC"
	case "price":
		orderBy = "p.price ASC"
	case "price_desc":
		orderBy = "p.price DESC"
	case "stock":
		orderBy = "p.stock ASC"
	case "stock_desc":
		orderBy = "p.stock DESC"
	}

	page := util.Atoi(q.Get("page"), 1)
	perPage := util.Atoi(q.Get("per_page"), 24)
	if perPage > 200 {
		perPage = 200
	}
	offset := (page - 1) * perPage

	var total int
	db.Pool.QueryRow(r.Context(), "SELECT COUNT(*) FROM products p WHERE "+where, args...).Scan(&total)

	query := fmt.Sprintf(`SELECT %s
        FROM products p WHERE %s ORDER BY %s LIMIT $%d OFFSET $%d`, db.ProductColumns, where, orderBy, idx, idx+1)
	args = append(args, perPage, offset)

	products := db.QueryProducts(r.Context(), query, args...)
	cats := db.QueryDistinct("SELECT DISTINCT category FROM products WHERE active = true")
	subCats := db.QueryDistinct("SELECT DISTINCT sub_category FROM products WHERE active = true")
	brands := db.QueryDistinct("SELECT DISTINCT brand FROM products WHERE active = true")
	materials := db.QueryDistinct("SELECT DISTINCT material FROM products WHERE active = true AND material IS NOT NULL")

	return map[string]interface{}{
		"products": products, "total": total, "page": page, "per_page": perPage,
		"categories": cats, "subCategories": subCats, "brands": brands, "materials": materials,
		"settings": db.LoadPublicSettings(r.Context()),
	}
}

func HandleGetProduct(w http.ResponseWriter, r *http.Request) {
	sku := chi.URLParam(r, "sku")
	cacheKey := "cache:products:sku:" + sku
	if cached, ok := middleware.CacheGet(r.Context(), cacheKey); ok {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(cached))
		return
	}

	var p db.ProductRow
	err := db.ScanProduct(db.Pool.QueryRow(r.Context(),
		fmt.Sprintf(`SELECT %s FROM products WHERE (sku = $1 OR id = $1) AND active = true`, db.ProductColumns), sku,
	), &p)
	if err != nil {
		util.JsonErr(w, 404, "Product not found")
		return
	}
	tiers := db.QueryActiveTiers(r.Context())
	related := db.QueryRelated(r.Context(), p.Category, p.SubCategory, p.ID)
	priceBreaks := db.QueryPriceBreaks(r.Context(), p.ID)

	data := map[string]interface{}{"product": p, "tiers": tiers, "priceBreaks": priceBreaks, "related": related, "success": true}
	b, err := json.Marshal(data)
	if err != nil {
		util.JsonOK(w, 200, data)
		return
	}
	middleware.CacheSet(r.Context(), cacheKey, string(b), 120*time.Second)
	w.Header().Set("Content-Type", "application/json")
	w.Write(b)
}

// ===== Public Price Tiers =====

func HandleListTiers(w http.ResponseWriter, r *http.Request) {
	writeCached(w, r, "cache:tiers:list", 300*time.Second, func() map[string]interface{} {
		rows, _ := db.Pool.Query(r.Context(), "SELECT id,label,min_qty,discount_percent,active,created_at,updated_at FROM price_tiers WHERE active = true ORDER BY min_qty ASC")
		defer rows.Close()
		var tiers []db.PriceTierRow
		for rows.Next() {
			var t db.PriceTierRow
			rows.Scan(&t.ID, &t.Label, &t.MinQty, &t.DiscountPercent, &t.Active, &t.CreatedAt, &t.UpdatedAt)
			tiers = append(tiers, t)
		}
		return map[string]interface{}{"tiers": tiers}
	})
}
