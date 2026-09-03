package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/cutmax/cutmax-backend/internal/db"
	"github.com/cutmax/cutmax-backend/internal/middleware"
	"github.com/cutmax/cutmax-backend/internal/storage"
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
		err := db.ScanProduct(db.Pool.QueryRow(r.Context(),
			fmt.Sprintf(`SELECT %s FROM products WHERE id=$1`, db.ProductColumns), id,
		), &p)
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
	query := fmt.Sprintf(`SELECT %s FROM products p WHERE %s ORDER BY p.created_at DESC LIMIT $%d OFFSET $%d`, db.ProductColumns, where, idx, idx+1)
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
		LowStockThreshold                                    *int `json:"lowStockThreshold"`
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
	// Always created inactive, same rule as bulk imports: a product needs a
	// real image before it can go live, and none has been uploaded yet at
	// the point this row is inserted (the frontend uploads it, if any, in a
	// separate follow-up call right after creating).
	_, err := db.Pool.Exec(r.Context(),
		`INSERT INTO products (id,sku,name,category,sub_category,brand,description,price,stock,unit,featured,active,sort_order,material,specifications,low_stock_threshold,created_at,updated_at)
         VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,false,$12,$13,$14::jsonb,$15,NOW(),NOW())`,
		pid, input.SKU, input.Name, input.Category, input.SubCategory, util.OrDef(input.Brand, "CUT-STOCK"), input.Description,
		input.Price, input.Stock, util.OrDef(input.Unit, "NOS"), input.Featured, input.SortOrder, material, specsJSON, input.LowStockThreshold,
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
	adminID, _ := r.Context().Value(middleware.AdminIDKey).(string)
	db.WriteAudit(r.Context(), &adminID, "product_create", fmt.Sprintf("%s (%s)", input.SKU, input.Name), "OK", middleware.ClientIP(r), r.Header.Get("User-Agent"))
	util.JsonOK(w, 201, map[string]interface{}{
		"product": map[string]interface{}{"id": pid, "sku": input.SKU, "name": input.Name, "category": input.Category, "subCategory": input.SubCategory},
	})
}

// writeProductAudit looks up a product's SKU/name for a readable audit detail
// (the update/delete handlers only have the opaque id from the URL).
func writeProductAudit(r *http.Request, action, productID string) {
	var sku, name string
	db.Pool.QueryRow(r.Context(), "SELECT sku,name FROM products WHERE id=$1", productID).Scan(&sku, &name)
	adminID, _ := r.Context().Value(middleware.AdminIDKey).(string)
	db.WriteAudit(r.Context(), &adminID, action, fmt.Sprintf("%s (%s)", sku, name), "OK", middleware.ClientIP(r), r.Header.Get("User-Agent"))
}

// activationBlockedReason returns why a product can't go active, or "" if
// it's fine to activate. Requires both a real price and an uploaded image --
// a product with neither shouldn't be sellable, and this is the one place
// both the single-product and bulk activation paths check it, so the rule
// can't drift between them. priceOverride is the price from the current
// request body, if the caller is also changing it in the same call.
func activationBlockedReason(ctx context.Context, productID string, priceOverride *float64) string {
	var price float64
	var imageURL *string
	if err := db.Pool.QueryRow(ctx, "SELECT price, image_url FROM products WHERE id=$1", productID).Scan(&price, &imageURL); err != nil {
		return "Product not found"
	}
	if priceOverride != nil {
		price = *priceOverride
	}
	if price <= 0 && (imageURL == nil || *imageURL == "") {
		return "Set a price and upload an image before activating this product"
	}
	if price <= 0 {
		return "Set a price before activating this product"
	}
	if imageURL == nil || *imageURL == "" {
		return "Upload an image before activating this product"
	}
	return ""
}

// HandleAdminProductsBulkActivate activates every selected product that
// already has a price and an image, and reports which ones it skipped (and
// why) so the admin can go fix just those instead of guessing.
func HandleAdminProductsBulkActivate(w http.ResponseWriter, r *http.Request) {
	var input struct {
		IDs []string `json:"ids"`
	}
	if err := util.Decode(r, &input); err != nil || len(input.IDs) == 0 {
		util.JsonErr(w, 400, "ids array required")
		return
	}
	adminID, _ := r.Context().Value(middleware.AdminIDKey).(string)
	ip, ua := middleware.ClientIP(r), r.Header.Get("User-Agent")

	activated := 0
	skipped := []map[string]string{}
	for _, id := range input.IDs {
		if reason := activationBlockedReason(r.Context(), id, nil); reason != "" {
			var sku string
			db.Pool.QueryRow(r.Context(), "SELECT sku FROM products WHERE id=$1", id).Scan(&sku)
			skipped = append(skipped, map[string]string{"sku": sku, "reason": reason})
			continue
		}
		db.Pool.Exec(r.Context(), "UPDATE products SET active=true, updated_at=NOW() WHERE id=$1", id)
		writeProductAudit(r, "product_activate", id)
		activated++
	}
	db.WriteAudit(r.Context(), &adminID, "bulk_product_activate", fmt.Sprintf("%d activated, %d skipped", activated, len(skipped)), "OK", ip, ua)
	middleware.CacheDelPattern(r.Context(), "cache:products:*")
	util.JsonOK(w, 200, map[string]interface{}{"activated": activated, "skipped": skipped})
}

// HandleAdminProductsBulkDelete permanently deletes multiple products at
// once (selected via checkboxes in the admin product list), same semantics
// as the single-product ?permanent=true delete: real row removal plus its
// uploaded image, not a deactivate.
func HandleAdminProductsBulkDelete(w http.ResponseWriter, r *http.Request) {
	var input struct {
		IDs []string `json:"ids"`
	}
	if err := util.Decode(r, &input); err != nil || len(input.IDs) == 0 {
		util.JsonErr(w, 400, "ids array required")
		return
	}
	adminID, _ := r.Context().Value(middleware.AdminIDKey).(string)
	ip, ua := middleware.ClientIP(r), r.Header.Get("User-Agent")

	deleted := 0
	for _, id := range input.IDs {
		var sku, name string
		var imageURL *string
		if err := db.Pool.QueryRow(r.Context(), "SELECT sku,name,image_url FROM products WHERE id=$1", id).Scan(&sku, &name, &imageURL); err != nil {
			continue
		}
		if _, err := db.Pool.Exec(r.Context(), "DELETE FROM products WHERE id=$1", id); err != nil {
			continue
		}
		if imageURL != nil && *imageURL != "" {
			if key := keyFromURL(*imageURL); key != "" {
				storage.Active.Delete(r.Context(), key)
			}
		}
		db.WriteAudit(r.Context(), &adminID, "product_delete", fmt.Sprintf("%s (%s)", sku, name), "OK", ip, ua)
		deleted++
	}
	middleware.CacheDelPattern(r.Context(), "cache:products:*")
	util.JsonOK(w, 200, map[string]interface{}{"deleted": deleted, "requested": len(input.IDs)})
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
			if v {
				var priceOverride *float64
				if p, priceOk := input["price"].(float64); priceOk {
					priceOverride = &p
				}
				if reason := activationBlockedReason(r.Context(), id, priceOverride); reason != "" {
					util.JsonErr(w, 400, reason)
					return
				}
			}
			addSet("active", v)
		}
		if v, ok := input["material"].(string); ok {
			if v == "" {
				addSet("material", nil)
			} else {
				addSet("material", v)
			}
		}
		if raw, ok := input["lowStockThreshold"]; ok {
			if raw == nil {
				addSet("low_stock_threshold", nil) // explicit null clears it -- falls back to the global default
			} else if v, ok := raw.(float64); ok {
				addSet("low_stock_threshold", int(v))
			}
		}
		if v, ok := input["specifications"]; ok {
			specsJSON, _ := json.Marshal(v)
			sets = append(sets, fmt.Sprintf("specifications=$%d::jsonb", idx))
			args = append(args, specsJSON)
			idx++
		}
		if remove, _ := input["removeImage"].(bool); remove {
			var oldURL *string
			db.Pool.QueryRow(r.Context(), "SELECT image_url FROM products WHERE id=$1", id).Scan(&oldURL)
			if oldURL != nil && *oldURL != "" {
				if key := keyFromURL(*oldURL); key != "" {
					storage.Active.Delete(r.Context(), key)
				}
			}
			addSet("image_url", nil)
			sets = append(sets, "image_type='PLACEHOLDER'", "active=false") // can't stay active with no image
		}
		sets = append(sets, "updated_at=NOW()")
		args = append(args, id)
		db.Pool.Exec(r.Context(), fmt.Sprintf("UPDATE products SET %s WHERE id=$%d", strings.Join(sets, ","), idx), args...)
		middleware.CacheDelPattern(r.Context(), "cache:products:*")
		writeProductAudit(r, "product_update", id)
		util.JsonOK(w, 200, map[string]interface{}{"message": "Product updated"})
		return
	}
	if r.Method == "DELETE" {
		if r.URL.Query().Get("permanent") == "true" {
			var imageURL *string
			db.Pool.QueryRow(r.Context(), "SELECT image_url FROM products WHERE id=$1", id).Scan(&imageURL)
			writeProductAudit(r, "product_delete", id)
			if _, err := db.Pool.Exec(r.Context(), "DELETE FROM products WHERE id=$1", id); err != nil {
				util.JsonErr(w, 500, "Failed to delete product")
				return
			}
			if imageURL != nil && *imageURL != "" {
				if key := keyFromURL(*imageURL); key != "" {
					storage.Active.Delete(r.Context(), key)
				}
			}
			middleware.CacheDelPattern(r.Context(), "cache:products:*")
			util.JsonOK(w, 200, map[string]interface{}{"message": "Product permanently deleted"})
			return
		}
		writeProductAudit(r, "product_deactivate", id)
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
