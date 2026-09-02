package handlers

import (
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/xuri/excelize/v2"

	"github.com/cutmax/cutmax-backend/internal/config"
	"github.com/cutmax/cutmax-backend/internal/db"
	"github.com/cutmax/cutmax-backend/internal/storage"
	"github.com/cutmax/cutmax-backend/internal/util"
)

// ===== Admin Bulk Prices =====

func HandleBulkPrices(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Prices []struct {
			ID    string  `json:"id"`
			Price float64 `json:"price"`
		} `json:"prices"`
	}
	if err := util.Decode(r, &input); err != nil || len(input.Prices) == 0 {
		util.JsonErr(w, 400, "prices array required")
		return
	}
	updated := 0
	skipped := 0
	for _, p := range input.Prices {
		tag, _ := db.Pool.Exec(r.Context(), "UPDATE products SET price=$1,updated_at=NOW() WHERE id=$2", p.Price, p.ID)
		if tag.RowsAffected() == 0 {
			skipped++
		} else {
			updated++
		}
	}
	util.JsonOK(w, 200, map[string]interface{}{"updated": updated, "skipped": skipped})
}

// ===== Admin Bulk Product Import (XLSX) =====

func HandleBulkProducts(w http.ResponseWriter, r *http.Request) {
	file, _, err := r.FormFile("file")
	if err != nil {
		util.JsonErr(w, 400, "file required")
		return
	}
	defer file.Close()
	f, err := excelize.OpenReader(file)
	if err != nil {
		util.JsonErr(w, 422, "Could not parse file as XLSX")
		return
	}
	defer f.Close()
	rows, err := f.GetRows(f.GetSheetName(0))
	if err != nil || len(rows) < 2 {
		util.JsonErr(w, 422, "Empty or invalid spreadsheet")
		return
	}

	header := rows[0]
	inserted, updated, skipped := 0, 0, 0
	errors := []map[string]interface{}{}

	for i, row := range rows[1:] {
		if len(row) < 4 {
			skipped++
			continue
		}
		rec := map[string]string{}
		for j, h := range header {
			if j < len(row) {
				rec[h] = row[j]
			}
		}
		sku := rec["sku"]
		if sku == "" {
			skipped++
			continue
		}
		price, _ := strconv.ParseFloat(rec["price"], 64)
		stock, _ := strconv.Atoi(rec["stock"])
		brand := rec["brand"]
		if brand == "" {
			brand = "CUT-STOCK"
		}
		var existingID string
		err := db.Pool.QueryRow(r.Context(), "SELECT id FROM products WHERE sku=$1", sku).Scan(&existingID)
		if err == pgx.ErrNoRows {
			// New products from a bulk import start inactive — they won't show on
			// the storefront until an image is attached via the bulk image upload
			// (or the regular edit form), so half-finished imports can't go live
			// with a placeholder image.
			_, err = db.Pool.Exec(r.Context(),
				`INSERT INTO products (id,sku,name,category,sub_category,brand,description,price,stock,unit,active,created_at,updated_at) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,false,NOW(),NOW())`,
				uuid.New().String(), sku, rec["name"], rec["category"], rec["subCategory"], brand, rec["description"], price, stock, util.OrDef(rec["unit"], "NOS"))
			if err != nil {
				errors = append(errors, map[string]interface{}{"row": i + 2, "sku": sku, "error": err.Error()})
				skipped++
			} else {
				inserted++
			}
		} else {
			_, err = db.Pool.Exec(r.Context(), "UPDATE products SET name=$1,category=$2,sub_category=$3,brand=$4,price=$5,stock=$6,updated_at=NOW() WHERE sku=$7",
				rec["name"], rec["category"], rec["subCategory"], brand, price, stock, sku)
			if err != nil {
				errors = append(errors, map[string]interface{}{"row": i + 2, "sku": sku, "error": err.Error()})
				skipped++
			} else {
				updated++
			}
		}
	}
	util.JsonOK(w, 200, map[string]interface{}{"inserted": inserted, "updated": updated, "skipped": skipped, "errors": errors})
}

// ===== Admin Bulk Stock Import =====
//
// Real supplier/stock sheets rarely match the strict sku/name/category
// headers HandleBulkProducts expects, and often have no SKU column at all —
// just an item name and a quantity. This importer accepts a looser set of
// header aliases and matches rows to existing products by name (since that's
// the only column guaranteed to line up with what's already in the catalog).
// A name with no existing match creates a new, inactive product with an
// auto-generated SKU, same "needs an image before going live" rule as
// HandleBulkProducts.

var stockHeaderAliases = map[string][]string{
	"name":        {"item", "name", "product", "productname", "itemname"},
	"category":    {"category", "cotegry", "categry", "cat"},
	"subCategory": {"subcategory", "subcotegry", "subcat", "subcategry"},
	"brand":       {"brand", "make"},
	"stock":       {"qty", "quantity", "stock", "stockqty"},
	"sku":         {"sku", "code", "itemcode"},
}

func normalizeStockHeader(h string) string {
	h = strings.ToLower(strings.TrimSpace(h))
	return strings.NewReplacer(" ", "", "-", "", "_", "").Replace(h)
}

// isStockPlaceholder catches sheets where a blank cell inherited the header
// text itself (e.g. a "SUB-COTEGRY" literal sitting in a sub-category cell).
func isStockPlaceholder(v string) bool {
	n := normalizeStockHeader(v)
	for _, aliases := range stockHeaderAliases {
		for _, a := range aliases {
			if n == a {
				return true
			}
		}
	}
	return false
}

func mapStockColumns(header []string) map[string]int {
	cols := map[string]int{}
	for i, h := range header {
		norm := normalizeStockHeader(h)
		for field, aliases := range stockHeaderAliases {
			if _, already := cols[field]; already {
				continue
			}
			for _, alias := range aliases {
				if norm == alias {
					cols[field] = i
				}
			}
		}
	}
	return cols
}

func HandleBulkStock(w http.ResponseWriter, r *http.Request) {
	file, _, err := r.FormFile("file")
	if err != nil {
		util.JsonErr(w, 400, "file required")
		return
	}
	defer file.Close()
	f, err := excelize.OpenReader(file)
	if err != nil {
		util.JsonErr(w, 422, "Could not parse file as XLSX")
		return
	}
	defer f.Close()
	rows, err := f.GetRows(f.GetSheetName(0))
	if err != nil || len(rows) < 2 {
		util.JsonErr(w, 422, "Empty or invalid spreadsheet")
		return
	}

	cols := mapStockColumns(rows[0])
	if _, ok := cols["name"]; !ok {
		util.JsonErr(w, 422, "Could not find an item/name column in the spreadsheet")
		return
	}

	get := func(row []string, field string) string {
		col, ok := cols[field]
		if !ok || col >= len(row) {
			return ""
		}
		v := strings.TrimSpace(row[col])
		if isStockPlaceholder(v) {
			return ""
		}
		return v
	}

	existing := db.LoadAllProducts(r.Context())
	byName := map[string]*db.ProductRow{}
	nextSKU := 1
	for i := range existing {
		byName[strings.ToLower(strings.TrimSpace(existing[i].Name))] = &existing[i]
		var n int
		if _, err := fmt.Sscanf(existing[i].SKU, "CT-%d", &n); err == nil && n >= nextSKU {
			nextSKU = n + 1
		}
	}

	updated, created, skipped := 0, 0, 0
	createdSKUs := []map[string]string{}
	errs := []map[string]interface{}{}

	for i, row := range rows[1:] {
		name := get(row, "name")
		if name == "" {
			skipped++
			continue
		}
		stock, _ := strconv.Atoi(strings.TrimSpace(get(row, "stock")))

		if p, found := byName[strings.ToLower(name)]; found {
			if _, err := db.Pool.Exec(r.Context(), "UPDATE products SET stock=$1,updated_at=NOW() WHERE id=$2", stock, p.ID); err != nil {
				errs = append(errs, map[string]interface{}{"row": i + 2, "name": name, "error": err.Error()})
				skipped++
				continue
			}
			updated++
			continue
		}

		sku := fmt.Sprintf("CT-%05d", nextSKU)
		nextSKU++
		category := util.OrDef(get(row, "category"), "Uncategorized")
		subCategory := util.OrDef(get(row, "subCategory"), "General")
		brand := util.OrDef(get(row, "brand"), "CUT-STOCK")
		if _, err := db.Pool.Exec(r.Context(),
			`INSERT INTO products (id,sku,name,category,sub_category,brand,description,price,stock,unit,active,created_at,updated_at) VALUES ($1,$2,$3,$4,$5,$6,'',0,$7,'NOS',false,NOW(),NOW())`,
			uuid.New().String(), sku, name, category, subCategory, brand, stock); err != nil {
			errs = append(errs, map[string]interface{}{"row": i + 2, "name": name, "error": err.Error()})
			skipped++
			continue
		}
		created++
		createdSKUs = append(createdSKUs, map[string]string{"sku": sku, "name": name})
	}

	util.JsonOK(w, 200, map[string]interface{}{
		"updated": updated, "created": created, "skipped": skipped,
		"createdProducts": createdSKUs, "errors": errs,
	})
}

// ===== Admin Bulk Image Import =====

func HandleBulkImages(w http.ResponseWriter, r *http.Request) {
	r.ParseMultipartForm(int64(config.Cfg.MaxUploadMB) << 20)
	files := r.MultipartForm.File["images"]
	if len(files) == 0 {
		util.JsonErr(w, 400, "No files found under field 'images'")
		return
	}

	products := db.LoadAllProducts(r.Context())
	matched := []map[string]string{}
	unmatched := []string{}
	errors := []map[string]string{}

	for _, fh := range files {
		f, err := fh.Open()
		if err != nil {
			errors = append(errors, map[string]string{"filename": fh.Filename, "error": err.Error()})
			continue
		}
		data, _ := io.ReadAll(f)
		f.Close()

		product := db.MatchProductByFilename(fh.Filename, products)
		if product == nil {
			unmatched = append(unmatched, fh.Filename)
			continue
		}

		mime, ext, ok := sniffFile(data)
		if !ok {
			errors = append(errors, map[string]string{"filename": fh.Filename, "error": "Unsupported file type"})
			continue
		}

		key := buildKey(product.SKU, ext)
		url, err := storage.Active.Save(r.Context(), key, data, mime)
		if err != nil {
			errors = append(errors, map[string]string{"filename": fh.Filename, "error": err.Error()})
			continue
		}

		// Delete old uploaded image if exists
		var oldURL *string
		db.Pool.QueryRow(r.Context(), "SELECT image_url FROM products WHERE id=$1", product.ID).Scan(&oldURL)
		if oldURL != nil && *oldURL != "" {
			if oldKey := keyFromURL(*oldURL); oldKey != "" {
				storage.Active.Delete(r.Context(), oldKey)
			}
		}

		// Uploading an image is what brings a bulk-imported product live.
		db.Pool.Exec(r.Context(), "UPDATE products SET image_url=$1,image_type='UPLOADED',active=true,updated_at=NOW() WHERE id=$2", url, product.ID)
		matched = append(matched, map[string]string{"filename": fh.Filename, "sku": product.SKU})
	}

	util.JsonOK(w, 200, map[string]interface{}{
		"matched": matched, "unmatched": unmatched, "errors": errors,
		"summary": map[string]interface{}{"total": len(files), "matched": len(matched), "unmatched": len(unmatched), "errors": len(errors)},
	})
}
