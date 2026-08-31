package handlers

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/cutmax/cutmax-backend/internal/db"
	"github.com/cutmax/cutmax-backend/internal/middleware"
	"github.com/cutmax/cutmax-backend/internal/util"
)

// ===== Admin Products CRUD =====

func HandleAdminProducts(w http.ResponseWriter, r *http.Request) {
	if r.Method == "POST" {
		createProduct(w, r)
		return
	}
	q := r.URL.Query()
	id := q.Get("id")
	if id != "" {
		var p db.ProductRow
		err := db.Pool.QueryRow(r.Context(),
			`SELECT id,sku,name,category,sub_category,brand,description,price,stock,unit,image_url,image_type,featured,active,sort_order,material,specifications,created_at,updated_at FROM products WHERE id=$1`, id,
		).Scan(&p.ID, &p.SKU, &p.Name, &p.Category, &p.SubCategory, &p.Brand, &p.Description, &p.Price, &p.Stock, &p.Unit, &p.ImageURL, &p.ImageType, &p.Featured, &p.Active, &p.SortOrder, &p.Material, &p.Specifications, &p.CreatedAt, &p.UpdatedAt)
		if err != nil {
			util.JsonOK(w, 200, map[string]interface{}{"product": nil})
			return
		}
		priceBreaks := db.QueryPriceBreaks(r.Context(), p.ID)
		util.JsonOK(w, 200, map[string]interface{}{"product": p, "priceBreaks": priceBreaks})
		return
	}

	where := "1=1"
	args := []interface{}{}
	idx := 1
	active := q.Get("active")
	if active == "true" {
		where += fmt.Sprintf(" AND p.active=$%d", idx)
		args = append(args, true)
		idx++
	} else if active == "false" {
		where += fmt.Sprintf(" AND p.active=$%d", idx)
		args = append(args, false)
		idx++
	}
	if v := q.Get("q"); v != "" {
		where += fmt.Sprintf(" AND (p.name ILIKE '%%'||$%d||'%%' OR p.sku ILIKE '%%'||$%d||'%%')", idx, idx)
		args = append(args, v)
		idx++
	}
	if v := q.Get("category"); v != "" {
		where += fmt.Sprintf(" AND p.category=$%d", idx)
		args = append(args, v)
		idx++
	}
	if v := q.Get("sub_category"); v != "" {
		where += fmt.Sprintf(" AND p.sub_category=$%d", idx)
		args = append(args, v)
		idx++
	}
	if v := q.Get("brand"); v != "" {
		where += fmt.Sprintf(" AND p.brand=$%d", idx)
		args = append(args, v)
		idx++
	}
	if v := q.Get("material"); v != "" {
		where += fmt.Sprintf(" AND p.material=$%d", idx)
		args = append(args, v)
		idx++
	}

	page := util.Atoi(q.Get("page"), 1)
	perPage := util.Atoi(q.Get("per_page"), 50)
	offset := (page - 1) * perPage

	var total int
	db.Pool.QueryRow(r.Context(), "SELECT COUNT(*) FROM products p WHERE "+where, args...).Scan(&total)
	query := fmt.Sprintf(`SELECT id,sku,name,category,sub_category,brand,description,price,stock,unit,image_url,image_type,featured,active,sort_order,material,specifications,created_at,updated_at FROM products p WHERE %s ORDER BY p.created_at DESC LIMIT $%d OFFSET $%d`, where, idx, idx+1)
	args = append(args, perPage, offset)
	products := db.QueryProducts(r.Context(), query, args...)
	util.JsonOK(w, 200, map[string]interface{}{"products": products, "total": total, "page": page, "per_page": perPage})
}

func createProduct(w http.ResponseWriter, r *http.Request) {
	var input struct {
		SKU, Name, Category, SubCategory, Brand, Description string
		Price                                                float64
		Stock                                                int
		Unit                                                 string
		Featured                                             bool
		SortOrder                                            int
		Material                                             string
		Specifications                                       []struct {
			Label string `json:"label"`
			Value string `json:"value"`
		}
	}
	if err := util.Decode(r, &input); err != nil || input.SKU == "" || input.Name == "" {
		util.JsonErr(w, 400, "SKU, name, category and price required")
		return
	}
	var material *string
	if input.Material != "" {
		material = &input.Material
	}
	specsJSON, _ := json.Marshal(input.Specifications)
	var pid string
	pid = uuid.New().String()
	_, err := db.Pool.Exec(r.Context(),
		`INSERT INTO products (id,sku,name,category,sub_category,brand,description,price,stock,unit,featured,sort_order,material,specifications,created_at,updated_at)
         VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14::jsonb,NOW(),NOW())`,
		pid, input.SKU, input.Name, input.Category, input.SubCategory, util.OrDef(input.Brand, "CUT-STOCK"), input.Description,
		input.Price, input.Stock, util.OrDef(input.Unit, "NOS"), input.Featured, input.SortOrder, material, specsJSON,
	)
	if err != nil {
		log.Printf("[admin] create product error: %v", err)
		if strings.Contains(err.Error(), "unique") {
			util.JsonErr(w, 409, "SKU already exists")
			return
		}
		util.JsonErr(w, 500, "Failed to create product")
		return
	}
	middleware.CacheDelPattern(r.Context(), "cache:products:*")
	util.JsonOK(w, 201, map[string]interface{}{
		"product": map[string]interface{}{"id": pid, "sku": input.SKU, "name": input.Name, "category": input.Category, "subCategory": input.SubCategory},
	})
}

func HandleAdminProduct(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if r.Method == "PUT" {
		var input map[string]interface{}
		if err := util.Decode(r, &input); err != nil {
			util.JsonErr(w, 400, "Invalid JSON")
			return
		}
		if sku, ok := input["sku"].(string); ok && sku != "" {
			var exists bool
			db.Pool.QueryRow(r.Context(), "SELECT EXISTS(SELECT 1 FROM products WHERE sku=$1 AND id!=$2)", sku, id).Scan(&exists)
			if exists {
				util.JsonErr(w, 409, "SKU already exists")
				return
			}
		}

		sets := []string{}
		args := []interface{}{}
		idx := 1
		addSet := func(col string, val interface{}) {
			sets = append(sets, fmt.Sprintf("%s=$%d", col, idx))
			args = append(args, val)
			idx++
		}
		if v, ok := input["sku"].(string); ok && v != "" {
			addSet("sku", v)
		}
		if v, ok := input["name"].(string); ok && v != "" {
			addSet("name", v)
		}
		if v, ok := input["category"].(string); ok && v != "" {
			addSet("category", v)
		}
		if v, ok := input["subCategory"].(string); ok && v != "" {
			addSet("sub_category", v)
		}
		if v, ok := input["brand"].(string); ok && v != "" {
			addSet("brand", v)
		}
		if v, ok := input["description"].(string); ok {
			addSet("description", v)
		}
		if v, ok := input["price"].(float64); ok {
			addSet("price", v)
		}
		if v, ok := input["stock"].(float64); ok {
			addSet("stock", int(v))
		}
		if v, ok := input["unit"].(string); ok && v != "" {
			addSet("unit", v)
		}
		if v, ok := input["featured"].(bool); ok {
			addSet("featured", v)
		}
		if v, ok := input["active"].(bool); ok {
			addSet("active", v)
		}
		if v, ok := input["material"].(string); ok {
			if v == "" {
				addSet("material", nil)
			} else {
				addSet("material", v)
			}
		}
		if v, ok := input["specifications"]; ok {
			specsJSON, _ := json.Marshal(v)
			sets = append(sets, fmt.Sprintf("specifications=$%d::jsonb", idx))
			args = append(args, specsJSON)
			idx++
		}
		sets = append(sets, "updated_at=NOW()")
		args = append(args, id)
		db.Pool.Exec(r.Context(), fmt.Sprintf("UPDATE products SET %s WHERE id=$%d", strings.Join(sets, ","), idx), args...)
		middleware.CacheDelPattern(r.Context(), "cache:products:*")
		util.JsonOK(w, 200, map[string]interface{}{"message": "Product updated"})
		return
	}
	if r.Method == "DELETE" {
		db.Pool.Exec(r.Context(), "UPDATE products SET active=false, updated_at=NOW() WHERE id=$1", id)
		middleware.CacheDelPattern(r.Context(), "cache:products:*")
		util.JsonOK(w, 200, map[string]interface{}{"message": "Product deactivated"})
		return
	}
	util.JsonErr(w, 405, "Method not allowed")
}

// ===== Admin Product Price Breaks (per-product volume pricing) =====

func HandleAdminProductPriceBreaks(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if r.Method != "PUT" {
		util.JsonErr(w, 405, "Method not allowed")
		return
	}
	var input struct {
		Breaks []struct {
			MinQty    int     `json:"minQty"`
			UnitPrice float64 `json:"unitPrice"`
		} `json:"breaks"`
	}
	if err := util.Decode(r, &input); err != nil {
		util.JsonErr(w, 400, "Invalid JSON")
		return
	}
	breaks := make([]db.PriceBreakRow, 0, len(input.Breaks))
	for _, b := range input.Breaks {
		if b.MinQty <= 0 || b.UnitPrice < 0 {
			continue
		}
		breaks = append(breaks, db.PriceBreakRow{MinQty: b.MinQty, UnitPrice: b.UnitPrice})
	}
	if err := db.ReplacePriceBreaks(r.Context(), id, breaks); err != nil {
		util.JsonErr(w, 500, "Failed to save price breaks")
		return
	}
	middleware.CacheDelPattern(r.Context(), "cache:products:*")
	util.JsonOK(w, 200, map[string]interface{}{"priceBreaks": db.QueryPriceBreaks(r.Context(), id)})
}
