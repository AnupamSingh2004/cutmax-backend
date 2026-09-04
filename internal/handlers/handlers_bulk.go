package handlers

import (
	"encoding/json"
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
	"github.com/cutmax/cutmax-backend/internal/middleware"
	"github.com/cutmax/cutmax-backend/internal/storage"
	"github.com/cutmax/cutmax-backend/internal/util"
)

func writeBulkAudit(r *http.Request, action, detail string) {
	adminID, _ := r.Context().Value(middleware.AdminIDKey).(string)
	db.WriteAudit(r.Context(), &adminID, action, detail, "OK", middleware.ClientIP(r), r.Header.Get("User-Agent"))
}

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
	writeBulkAudit(r, "bulk_price_update", fmt.Sprintf("%d updated, %d skipped", updated, skipped))
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
		category, subCategory, material, needsReview := normalizeTaxonomy(rec["category"], rec["subCategory"])
		if needsReview {
			errors = append(errors, map[string]interface{}{
				"row": i + 2, "sku": sku,
				"error": fmt.Sprintf("Unrecognized category/subCategory (%q / %q) -- used as-is, please double check", rec["category"], rec["subCategory"]),
			})
		}
		var materialPtr *string
		if material != "" {
			materialPtr = &material
		}
		var existingID string
		err := db.Pool.QueryRow(r.Context(), "SELECT id FROM products WHERE sku=$1", sku).Scan(&existingID)
		if err == pgx.ErrNoRows {
			// New products from a bulk import start inactive — they won't show on
			// the storefront until an image is attached via the bulk image upload
			// (or the regular edit form), so half-finished imports can't go live
			// with a placeholder image.
			_, err = db.Pool.Exec(r.Context(),
				`INSERT INTO products (id,sku,name,category,sub_category,brand,description,price,stock,unit,material,active,created_at,updated_at) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,false,NOW(),NOW())`,
				uuid.New().String(), sku, rec["name"], category, subCategory, brand, rec["description"], price, stock, util.OrDef(rec["unit"], "NOS"), materialPtr)
			if err != nil {
				errors = append(errors, map[string]interface{}{"row": i + 2, "sku": sku, "error": err.Error()})
				skipped++
			} else {
				inserted++
			}
		} else {
			_, err = db.Pool.Exec(r.Context(), "UPDATE products SET name=$1,category=$2,sub_category=$3,brand=$4,price=$5,stock=$6,material=COALESCE($7,material),updated_at=NOW() WHERE sku=$8",
				rec["name"], category, subCategory, brand, price, stock, materialPtr, sku)
			if err != nil {
				errors = append(errors, map[string]interface{}{"row": i + 2, "sku": sku, "error": err.Error()})
				skipped++
			} else {
				updated++
			}
		}
	}
	writeBulkAudit(r, "bulk_product_import", fmt.Sprintf("%d inserted, %d updated, %d skipped", inserted, updated, skipped))
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
	"name":              {"item", "name", "product", "productname", "itemname"},
	"category":          {"category", "cotegry", "categry", "cat"},
	"subCategory":       {"subcategory", "subcotegry", "subcat", "subcategry"},
	"brand":             {"brand", "make"},
	"stock":             {"qty", "quantity", "stock", "stockqty"},
	"sku":               {"sku", "code", "itemcode"},
	"material":          {"material", "grade"},
	"description":       {"description", "desc"},
	"price":             {"price", "rate", "unitprice", "mrp"},
	"unit":              {"unit", "uom"},
	"featured":          {"featured"},
	"sortOrder":         {"sortorder", "order", "position", "displayorder"},
	"lowStockThreshold": {"lowstockthreshold", "lowstock", "reorderlevel", "reorderpoint"},
	"specifications":    {"specifications", "specs", "spec"},
}

// parseBoolCell reads a spreadsheet cell as a boolean -- "TRUE"/"YES"/"Y"/"1"
// (case-insensitive) count as true, everything else (including blank) as false.
func parseBoolCell(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "true", "yes", "y", "1":
		return true
	default:
		return false
	}
}

type specPair struct {
	Label string `json:"label"`
	Value string `json:"value"`
}

// parseSpecificationsCell reads a flat "Label: Value | Label2: Value2" cell
// into the same {label,value}[] shape the admin edit form's specifications
// table stores as JSON -- a spreadsheet has no room for a nested table, so
// this is the one-cell encoding for it.
func parseSpecificationsCell(s string) []byte {
	var specs []specPair
	for _, part := range strings.Split(s, "|") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		kv := strings.SplitN(part, ":", 2)
		if len(kv) != 2 {
			continue
		}
		label := strings.TrimSpace(kv[0])
		value := strings.TrimSpace(kv[1])
		if label == "" || value == "" {
			continue
		}
		specs = append(specs, specPair{Label: label, Value: value})
	}
	b, _ := json.Marshal(specs)
	return b
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
	bySKU := map[string]*db.ProductRow{}
	nextSKU := 1
	for i := range existing {
		byName[strings.ToLower(strings.TrimSpace(existing[i].Name))] = &existing[i]
		bySKU[strings.ToUpper(strings.TrimSpace(existing[i].SKU))] = &existing[i]
		var n int
		if _, err := fmt.Sscanf(existing[i].SKU, "CT-%d", &n); err == nil && n >= nextSKU {
			nextSKU = n + 1
		}
	}

	updated, created, skipped := 0, 0, 0
	createdSKUs := []map[string]string{}
	errs := []map[string]interface{}{}
	warnings := []map[string]interface{}{}

	for i, row := range rows[1:] {
		name := get(row, "name")
		skuVal := get(row, "sku")
		if name == "" && skuVal == "" {
			skipped++
			continue
		}

		var existingProduct *db.ProductRow
		if skuVal != "" {
			existingProduct = bySKU[strings.ToUpper(skuVal)]
		}
		if existingProduct == nil && name != "" {
			existingProduct = byName[strings.ToLower(name)]
		}

		rawCategory := get(row, "category")
		rawSubCategory := get(row, "subCategory")
		rawBrand := get(row, "brand")
		rawMaterial := get(row, "material")
		rawDescription := get(row, "description")
		rawPrice := get(row, "price")
		rawStock := get(row, "stock")
		rawUnit := get(row, "unit")
		rawFeatured := get(row, "featured")
		rawSortOrder := get(row, "sortOrder")
		rawLowStock := get(row, "lowStockThreshold")
		rawSpecs := get(row, "specifications")

		warn := func(field, value string) {
			warnings = append(warnings, map[string]interface{}{
				"row": i + 2, "name": name, "sku": skuVal, "field": field, "value": value,
				"message": "Not a recognized " + field + " -- used as-is, please double check",
			})
		}

		if existingProduct != nil {
			sets := []string{}
			args := []interface{}{}
			idx := 1
			addSet := func(col string, val interface{}) {
				sets = append(sets, fmt.Sprintf("%s=$%d", col, idx))
				args = append(args, val)
				idx++
			}
			if name != "" {
				addSet("name", name)
			}
			if rawCategory != "" {
				category, subCategory, material, needsReview := normalizeTaxonomy(rawCategory, rawSubCategory)
				addSet("category", category)
				addSet("sub_category", subCategory)
				if rawMaterial != "" {
					addSet("material", rawMaterial)
				} else if material != "" {
					addSet("material", material)
				}
				if needsReview {
					warn("category/subCategory", rawCategory+" / "+rawSubCategory)
				}
			} else if rawMaterial != "" {
				addSet("material", rawMaterial)
			}
			if rawBrand != "" {
				addSet("brand", rawBrand)
			}
			if rawDescription != "" {
				addSet("description", rawDescription)
			}
			if rawPrice != "" {
				if price, err := strconv.ParseFloat(rawPrice, 64); err == nil {
					addSet("price", price)
				}
			}
			if rawStock != "" {
				if stock, err := strconv.Atoi(rawStock); err == nil {
					addSet("stock", stock)
				}
			}
			if rawUnit != "" {
				addSet("unit", rawUnit)
			}
			if rawFeatured != "" {
				addSet("featured", parseBoolCell(rawFeatured))
			}
			if rawSortOrder != "" {
				if so, err := strconv.Atoi(rawSortOrder); err == nil {
					addSet("sort_order", so)
				}
			}
			if rawLowStock != "" {
				if ls, err := strconv.Atoi(rawLowStock); err == nil {
					addSet("low_stock_threshold", ls)
				}
			}
			if rawSpecs != "" {
				sets = append(sets, fmt.Sprintf("specifications=$%d::jsonb", idx))
				args = append(args, parseSpecificationsCell(rawSpecs))
				idx++
			}

			if len(sets) == 0 {
				skipped++
				continue
			}
			sets = append(sets, "updated_at=NOW()")
			args = append(args, existingProduct.ID)
			query := fmt.Sprintf("UPDATE products SET %s WHERE id=$%d", strings.Join(sets, ","), idx)
			if _, err := db.Pool.Exec(r.Context(), query, args...); err != nil {
				errs = append(errs, map[string]interface{}{"row": i + 2, "name": name, "error": err.Error()})
				skipped++
				continue
			}
			updated++
			continue
		}

		if name == "" {
			// A bare SKU with no matching existing product and no name can't
			// create a new row.
			skipped++
			continue
		}

		sku := skuVal
		if sku == "" {
			sku = fmt.Sprintf("CT-%05d", nextSKU)
			nextSKU++
		}
		category, subCategory, material, needsReview := normalizeTaxonomy(rawCategory, rawSubCategory)
		if rawMaterial != "" {
			material = rawMaterial
		}
		if needsReview {
			warn("category/subCategory", rawCategory+" / "+rawSubCategory)
		}
		category = util.OrDef(category, "Uncategorized")
		subCategory = util.OrDef(subCategory, "General")
		brand := util.OrDef(rawBrand, "CUT-STOCK")
		unit := util.OrDef(rawUnit, "NOS")
		var materialPtr *string
		if material != "" {
			materialPtr = &material
		}
		var lowStockPtr *int
		if rawLowStock != "" {
			if ls, err := strconv.Atoi(rawLowStock); err == nil {
				lowStockPtr = &ls
			}
		}
		stock, _ := strconv.Atoi(rawStock)
		price, _ := strconv.ParseFloat(rawPrice, 64)
		sortOrder, _ := strconv.Atoi(rawSortOrder)
		featured := parseBoolCell(rawFeatured)

		if _, err := db.Pool.Exec(r.Context(),
			`INSERT INTO products (id,sku,name,category,sub_category,brand,description,price,stock,unit,featured,active,sort_order,material,specifications,low_stock_threshold,created_at,updated_at)
			 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,false,$12,$13,$14::jsonb,$15,NOW(),NOW())`,
			uuid.New().String(), sku, name, category, subCategory, brand, rawDescription, price, stock, unit, featured, sortOrder, materialPtr, parseSpecificationsCell(rawSpecs), lowStockPtr,
		); err != nil {
			errs = append(errs, map[string]interface{}{"row": i + 2, "name": name, "error": err.Error()})
			skipped++
			continue
		}
		created++
		createdSKUs = append(createdSKUs, map[string]string{"sku": sku, "name": name})
	}

	writeBulkAudit(r, "bulk_stock_import", fmt.Sprintf("%d updated, %d created, %d skipped", updated, created, skipped))
	util.JsonOK(w, 200, map[string]interface{}{
		"updated": updated, "created": created, "skipped": skipped,
		"createdProducts": createdSKUs, "errors": errs, "warnings": warnings,
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
	needsPrice := []string{}

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

		// Uploading an image is what brings a bulk-imported product live --
		// but only once it also has a real price; a ₹0 product still needs
		// an admin to set one before it can go active.
		if product.Price > 0 {
			db.Pool.Exec(r.Context(), "UPDATE products SET image_url=$1,image_type='UPLOADED',active=true,updated_at=NOW() WHERE id=$2", url, product.ID)
		} else {
			db.Pool.Exec(r.Context(), "UPDATE products SET image_url=$1,image_type='UPLOADED',updated_at=NOW() WHERE id=$2", url, product.ID)
			needsPrice = append(needsPrice, product.SKU)
		}
		matched = append(matched, map[string]string{"filename": fh.Filename, "sku": product.SKU})
	}

	writeBulkAudit(r, "bulk_image_import", fmt.Sprintf("%d matched, %d unmatched", len(matched), len(unmatched)))
	util.JsonOK(w, 200, map[string]interface{}{
		"matched": matched, "unmatched": unmatched, "errors": errors, "needsPrice": needsPrice,
		"summary": map[string]interface{}{"total": len(files), "matched": len(matched), "unmatched": len(unmatched), "errors": len(errors)},
	})
}
