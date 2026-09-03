package db

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ===== Helpers =====

var Pool *pgxpool.Pool

// WriteAudit records an entry in the audit trail. adminID is nil for
// unauthenticated actions (e.g. a customer submitting an enquiry). Fire and
// forget: an audit-log failure shouldn't fail the request that triggered it,
// but is worth logging since a silently-broken audit trail defeats its point.
func WriteAudit(ctx context.Context, adminID *string, action, detail, status, ip, userAgent string) {
	_, err := Pool.Exec(ctx,
		"INSERT INTO audit_log (id,admin_id,action,detail,status,ip,user_agent,created_at) VALUES ($1,$2,$3,$4,$5,$6,$7,NOW())",
		uuid.New().String(), adminID, action, detail, status, ip, userAgent)
	if err != nil {
		log.Printf("[audit] failed to write %q: %v", action, err)
	}
}

type ProductRow struct {
	ID                string          `json:"id"`
	SKU               string          `json:"sku"`
	Name              string          `json:"name"`
	Category          string          `json:"category"`
	SubCategory       string          `json:"subCategory"`
	Brand             string          `json:"brand"`
	Description       string          `json:"description"`
	Price             float64         `json:"price"`
	Stock             int             `json:"stock"`
	Unit              string          `json:"unit"`
	ImageURL          *string         `json:"imageUrl"`
	ImageType         string          `json:"imageType"`
	Featured          bool            `json:"featured"`
	Active            bool            `json:"active"`
	SortOrder         int             `json:"sortOrder"`
	Material          *string         `json:"material"`
	Specifications    json.RawMessage `json:"specifications"`
	LowStockThreshold *int            `json:"lowStockThreshold"`
	CreatedAt         time.Time       `json:"createdAt"`
	UpdatedAt         time.Time       `json:"updatedAt"`
}

// ProductColumns is the canonical column list/order for every SELECT that
// scans into a ProductRow via ScanProduct. Defined once so adding a column
// (like low_stock_threshold) can't silently miss one of the several queries
// that read the products table -- that exact mistake (a missing `id` column)
// broke bulk imports before this was consolidated.
const ProductColumns = "id,sku,name,category,sub_category,brand,description,price,stock,unit,image_url,image_type,featured,active,sort_order,material,specifications,low_stock_threshold,created_at,updated_at"

// ScanProduct scans one products-table row (selected via ProductColumns, in
// that exact order) into p.
func ScanProduct(row pgx.Row, p *ProductRow) error {
	return row.Scan(&p.ID, &p.SKU, &p.Name, &p.Category, &p.SubCategory, &p.Brand, &p.Description, &p.Price, &p.Stock, &p.Unit, &p.ImageURL, &p.ImageType, &p.Featured, &p.Active, &p.SortOrder, &p.Material, &p.Specifications, &p.LowStockThreshold, &p.CreatedAt, &p.UpdatedAt)
}

type PriceTierRow struct {
	ID              string    `json:"id"`
	Label           string    `json:"label"`
	MinQty          int       `json:"minQty"`
	DiscountPercent float64   `json:"discountPercent"`
	Active          bool      `json:"active"`
	CreatedAt       time.Time `json:"createdAt"`
	UpdatedAt       time.Time `json:"updatedAt"`
}

// PriceBreakRow is a per-product volume-pricing breakpoint: "at minQty units
// or more, this product costs unitPrice each." Optional — a product with no
// rows just sells at its normal price at any quantity.
type PriceBreakRow struct {
	ID        string  `json:"id"`
	ProductID string  `json:"productId"`
	MinQty    int     `json:"minQty"`
	UnitPrice float64 `json:"unitPrice"`
}

func QueryPriceBreaks(ctx context.Context, productID string) []PriceBreakRow {
	rows, _ := Pool.Query(ctx, "SELECT id,product_id,min_qty,unit_price FROM product_price_breaks WHERE product_id=$1 ORDER BY min_qty ASC", productID)
	defer rows.Close()
	breaks := []PriceBreakRow{}
	for rows.Next() {
		var b PriceBreakRow
		rows.Scan(&b.ID, &b.ProductID, &b.MinQty, &b.UnitPrice)
		breaks = append(breaks, b)
	}
	return breaks
}

// ReplacePriceBreaks atomically replaces every price break row for a product.
func ReplacePriceBreaks(ctx context.Context, productID string, breaks []PriceBreakRow) error {
	tx, err := Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, "DELETE FROM product_price_breaks WHERE product_id=$1", productID); err != nil {
		return err
	}
	for _, b := range breaks {
		if _, err := tx.Exec(ctx,
			"INSERT INTO product_price_breaks (id,product_id,min_qty,unit_price) VALUES (gen_random_uuid()::text,$1,$2,$3)",
			productID, b.MinQty, b.UnitPrice,
		); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func QueryProducts(ctx context.Context, query string, args ...interface{}) []ProductRow {
	rows, err := Pool.Query(ctx, query, args...)
	if err != nil {
		return []ProductRow{}
	}
	defer rows.Close()
	products := []ProductRow{}
	for rows.Next() {
		var p ProductRow
		ScanProduct(rows, &p)
		products = append(products, p)
	}
	return products
}

func QueryDistinct(q string) []string {
	rows, _ := Pool.Query(context.Background(), q)
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var s string
		rows.Scan(&s)
		out = append(out, s)
	}
	return out
}

func QueryActiveTiers(ctx context.Context) []PriceTierRow {
	rows, _ := Pool.Query(ctx, "SELECT id,label,min_qty,discount_percent,active,created_at,updated_at FROM price_tiers WHERE active=true ORDER BY min_qty")
	defer rows.Close()
	tiers := []PriceTierRow{}
	for rows.Next() {
		var t PriceTierRow
		rows.Scan(&t.ID, &t.Label, &t.MinQty, &t.DiscountPercent, &t.Active, &t.CreatedAt, &t.UpdatedAt)
		tiers = append(tiers, t)
	}
	return tiers
}

func QueryRelated(ctx context.Context, category, subCategory, excludeID string) []ProductRow {
	rows, _ := Pool.Query(ctx,
		fmt.Sprintf(`SELECT %s FROM products WHERE active=true AND sub_category=$1 AND id!=$2 LIMIT 6`, ProductColumns), subCategory, excludeID)
	defer rows.Close()
	products := []ProductRow{}
	for rows.Next() {
		var p ProductRow
		ScanProduct(rows, &p)
		products = append(products, p)
	}
	return products
}

func LoadActiveProducts(ctx context.Context) []ProductRow {
	return QueryProducts(ctx, fmt.Sprintf("SELECT %s FROM products WHERE active=true", ProductColumns))
}

// LoadPublicSettings fetches every settings row in one query and returns the
// subset that's safe/relevant to expose publicly, with sane defaults for
// anything unset. Add a new key here (and to admin settings) to make it show
// up on the storefront -- see cutmax-frontend's PublicSettings type.
func LoadPublicSettings(ctx context.Context) map[string]interface{} {
	rows, err := Pool.Query(ctx, "SELECT key, value FROM settings")
	vals := map[string]string{}
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var k, v string
			if rows.Scan(&k, &v) == nil {
				vals[k] = v
			}
		}
	}
	get := func(key, def string) string {
		if v, ok := vals[key]; ok && v != "" {
			return v
		}
		return def
	}
	lowStock := 10
	if n, err := strconv.Atoi(vals["low_stock_limit"]); err == nil {
		lowStock = n
	}
	gstRate := 18.0
	if f, err := strconv.ParseFloat(vals["gst_percent"], 64); err == nil {
		gstRate = f
	}
	return map[string]interface{}{
		"whatsapp":                  get("whatsapp", ""),
		"gst_percent":               gstRate,
		"low_stock":                 lowStock,
		"hero_video_url":            get("hero_video_url", ""),
		"site_background_video_url": get("site_background_video_url", ""),
		"hero_title":                get("hero_title", ""),
		"hero_subtitle":             get("hero_subtitle", ""),
		"company_name":              get("company_name", ""),
		"company_address":           get("company_address", ""),
		"company_phone":             get("company_phone", ""),
	}
}

// LoadAllProducts includes inactive products too — needed so bulk image
// uploads can still match products that were auto-deactivated by a bulk
// import pending their first image (see HandleBulkProducts/HandleBulkImages).
func LoadAllProducts(ctx context.Context) []ProductRow {
	return QueryProducts(ctx, fmt.Sprintf("SELECT %s FROM products", ProductColumns))
}

func MatchProductByFilename(filename string, products []ProductRow) *ProductRow {
	name := strings.ToLower(strings.TrimSuffix(filename, filepath.Ext(filename)))
	name = regexp.MustCompile(`[^a-z0-9]`).ReplaceAllString(name, "")
	for i := range products {
		if strings.ToLower(products[i].SKU) == name {
			return &products[i]
		}
	}
	for i := range products {
		sku := strings.ToLower(products[i].SKU)
		if len(sku) >= 4 && (strings.Contains(name, sku) || strings.Contains(sku, name)) {
			return &products[i]
		}
	}
	for i := range products {
		pname := strings.ToLower(products[i].Name)
		pname = regexp.MustCompile(`[^a-z0-9]`).ReplaceAllString(pname, "")
		if pname == name {
			return &products[i]
		}
	}
	return nil
}
