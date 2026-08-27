package handlers

import (
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"

	"github.com/jackc/pgx/v5"
	"github.com/xuri/excelize/v2"

	"github.com/cutmax/cutmax-backend/internal/config"
	"github.com/cutmax/cutmax-backend/internal/db"
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
			_, err = db.Pool.Exec(r.Context(),
				`INSERT INTO products (sku,name,category,sub_category,brand,description,price,stock,unit,created_at,updated_at) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,NOW(),NOW())`,
				sku, rec["name"], rec["category"], rec["subCategory"], brand, rec["description"], price, stock, util.OrDef(rec["unit"], "NOS"))
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

// ===== Admin Bulk Image Import =====

func HandleBulkImages(w http.ResponseWriter, r *http.Request) {
	r.ParseMultipartForm(int64(config.Cfg.MaxUploadMB) << 20)
	files := r.MultipartForm.File["images"]
	if len(files) == 0 {
		util.JsonErr(w, 400, "No files found under field 'images'")
		return
	}

	products := db.LoadActiveProducts(r.Context())
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

		_, ext, ok := sniffFile(data)
		if !ok {
			errors = append(errors, map[string]string{"filename": fh.Filename, "error": "Unsupported file type"})
			continue
		}

		key := buildKey(product.SKU, ext)
		url, err := saveFile(key, data)
		if err != nil {
			errors = append(errors, map[string]string{"filename": fh.Filename, "error": err.Error()})
			continue
		}

		// Delete old uploaded image if exists
		var oldURL *string
		db.Pool.QueryRow(r.Context(), "SELECT image_url FROM products WHERE id=$1", product.ID).Scan(&oldURL)
		if oldURL != nil && *oldURL != "" {
			oldKey := keyFromURL(*oldURL)
			if oldKey != "" {
				os.Remove(filepath.Join(config.Cfg.UploadsDir, oldKey))
			}
		}

		db.Pool.Exec(r.Context(), "UPDATE products SET image_url=$1,image_type='UPLOADED',updated_at=NOW() WHERE id=$2", url, product.ID)
		matched = append(matched, map[string]string{"filename": fh.Filename, "sku": product.SKU})
	}

	util.JsonOK(w, 200, map[string]interface{}{
		"matched": matched, "unmatched": unmatched, "errors": errors,
		"summary": map[string]interface{}{"total": len(files), "matched": len(matched), "unmatched": len(unmatched), "errors": len(errors)},
	})
}
