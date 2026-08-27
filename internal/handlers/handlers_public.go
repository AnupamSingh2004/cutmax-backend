package handlers

import (
	"fmt"
	"math/rand"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/cutmax/cutmax-backend/internal/config"
	"github.com/cutmax/cutmax-backend/internal/db"
	"github.com/cutmax/cutmax-backend/internal/middleware"
	"github.com/cutmax/cutmax-backend/internal/util"
)

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

	query := fmt.Sprintf(`SELECT id,sku,name,category,sub_category,brand,description,price,stock,unit,image_url,image_type,featured,active,sort_order,created_at,updated_at
        FROM products p WHERE %s ORDER BY %s LIMIT $%d OFFSET $%d`, where, orderBy, idx, idx+1)
	args = append(args, perPage, offset)

	products := db.QueryProducts(r.Context(), query, args...)
	cats := db.QueryDistinct("SELECT DISTINCT category FROM products WHERE active = true")
	subCats := db.QueryDistinct("SELECT DISTINCT sub_category FROM products WHERE active = true")
	brands := db.QueryDistinct("SELECT DISTINCT brand FROM products WHERE active = true")

	// Settings used by the frontend
	var lowStock int
	db.Pool.QueryRow(r.Context(), "SELECT COALESCE(value::int,10) FROM settings WHERE key='low_stock_limit'").Scan(&lowStock)
	var gstRate float64
	db.Pool.QueryRow(r.Context(), "SELECT COALESCE(value::float,18) FROM settings WHERE key='gst_percent'").Scan(&gstRate)
	var whatsapp string
	db.Pool.QueryRow(r.Context(), "SELECT COALESCE(value,'') FROM settings WHERE key='whatsapp'").Scan(&whatsapp)

	util.JsonOK(w, 200, map[string]interface{}{
		"products": products, "total": total, "page": page, "per_page": perPage,
		"categories": cats, "subCategories": subCats, "brands": brands,
		"settings": map[string]interface{}{
			"whatsapp": whatsapp, "gst_percent": gstRate, "low_stock": lowStock,
		},
	})
}

func HandleGetProduct(w http.ResponseWriter, r *http.Request) {
	sku := chi.URLParam(r, "sku")
	var p db.ProductRow
	err := db.Pool.QueryRow(r.Context(),
		`SELECT id,sku,name,category,sub_category,brand,description,price,stock,unit,image_url,image_type,featured,active,sort_order,created_at,updated_at
         FROM products WHERE (sku = $1 OR id = $1) AND active = true`, sku,
	).Scan(&p.ID, &p.SKU, &p.Name, &p.Category, &p.SubCategory, &p.Brand, &p.Description, &p.Price, &p.Stock, &p.Unit, &p.ImageURL, &p.ImageType, &p.Featured, &p.Active, &p.SortOrder, &p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		util.JsonErr(w, 404, "Product not found")
		return
	}
	tiers := db.QueryActiveTiers(r.Context())
	related := db.QueryRelated(r.Context(), p.Category, p.SubCategory, p.ID)
	util.JsonOK(w, 200, map[string]interface{}{"product": p, "tiers": tiers, "related": related})
}

// ===== Public Price Tiers =====

func HandleListTiers(w http.ResponseWriter, r *http.Request) {
	rows, _ := db.Pool.Query(r.Context(), "SELECT id,label,min_qty,discount_percent,active,created_at,updated_at FROM price_tiers WHERE active = true ORDER BY min_qty ASC")
	defer rows.Close()
	var tiers []db.PriceTierRow
	for rows.Next() {
		var t db.PriceTierRow
		rows.Scan(&t.ID, &t.Label, &t.MinQty, &t.DiscountPercent, &t.Active, &t.CreatedAt, &t.UpdatedAt)
		tiers = append(tiers, t)
	}
	util.JsonOK(w, 200, map[string]interface{}{"tiers": tiers})
}
