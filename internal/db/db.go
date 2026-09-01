package db

import (
	"context"
	"encoding/json"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ===== Helpers =====

var Pool *pgxpool.Pool

type ProductRow struct {
	ID             string          `json:"id"`
	SKU            string          `json:"sku"`
	Name           string          `json:"name"`
	Category       string          `json:"category"`
	SubCategory    string          `json:"subCategory"`
	Brand          string          `json:"brand"`
	Description    string          `json:"description"`
	Price          float64         `json:"price"`
	Stock          int             `json:"stock"`
	Unit           string          `json:"unit"`
	ImageURL       *string         `json:"imageUrl"`
	ImageType      string          `json:"imageType"`
	Featured       bool            `json:"featured"`
	Active         bool            `json:"active"`
	SortOrder      int             `json:"sortOrder"`
	Material       *string         `json:"material"`
	Specifications json.RawMessage `json:"specifications"`
	CreatedAt      time.Time       `json:"createdAt"`
	UpdatedAt      time.Time       `json:"updatedAt"`
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

var scanProduct = func(row pgx.Row, p *ProductRow) error {
	return row.Scan(&p.ID, &p.SKU, &p.Name, &p.Category, &p.SubCategory, &p.Brand, &p.Description, &p.Price, &p.Stock, &p.Unit, &p.ImageURL, &p.ImageType, &p.Featured, &p.Active, &p.SortOrder, &p.Material, &p.Specifications, &p.CreatedAt, &p.UpdatedAt)
}

func QueryProducts(ctx context.Context, query string, args ...interface{}) []ProductRow {
	rows, err := Pool.Query(ctx, query, args...)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var products []ProductRow
	for rows.Next() {
		var p ProductRow
		rows.Scan(&p.ID, &p.SKU, &p.Name, &p.Category, &p.SubCategory, &p.Brand, &p.Description, &p.Price, &p.Stock, &p.Unit, &p.ImageURL, &p.ImageType, &p.Featured, &p.Active, &p.SortOrder, &p.Material, &p.Specifications, &p.CreatedAt, &p.UpdatedAt)
		products = append(products, p)
	}
	return products
}

func QueryDistinct(q string) []string {
	rows, _ := Pool.Query(context.Background(), q)
	defer rows.Close()
	var out []string
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
	var tiers []PriceTierRow
	for rows.Next() {
		var t PriceTierRow
		rows.Scan(&t.ID, &t.Label, &t.MinQty, &t.DiscountPercent, &t.Active, &t.CreatedAt, &t.UpdatedAt)
		tiers = append(tiers, t)
	}
	return tiers
}

func QueryRelated(ctx context.Context, category, subCategory, excludeID string) []ProductRow {
	rows, _ := Pool.Query(ctx,
		`SELECT id,sku,name,category,sub_category,brand,description,price,stock,unit,image_url,image_type,featured,active,sort_order,material,specifications,created_at,updated_at
         FROM products WHERE active=true AND sub_category=$1 AND id!=$2 LIMIT 6`, subCategory, excludeID)
	defer rows.Close()
	var products []ProductRow
	for rows.Next() {
		var p ProductRow
		rows.Scan(&p.ID, &p.SKU, &p.Name, &p.Category, &p.SubCategory, &p.Brand, &p.Description, &p.Price, &p.Stock, &p.Unit, &p.ImageURL, &p.ImageType, &p.Featured, &p.Active, &p.SortOrder, &p.Material, &p.Specifications, &p.CreatedAt, &p.UpdatedAt)
		products = append(products, p)
	}
	return products
}

func LoadActiveProducts(ctx context.Context) []ProductRow {
	return QueryProducts(ctx, "SELECT id,sku,name,category,sub_category,brand,description,price,stock,unit,image_url,image_type,featured,active,sort_order,material,specifications,created_at,updated_at FROM products WHERE active=true")
}

// LoadAllProducts includes inactive products too — needed so bulk image
// uploads can still match products that were auto-deactivated by a bulk
// import pending their first image (see HandleBulkProducts/HandleBulkImages).
func LoadAllProducts(ctx context.Context) []ProductRow {
	return QueryProducts(ctx, "SELECT id,sku,name,category,sub_category,brand,description,price,stock,unit,image_url,image_type,featured,active,sort_order,material,specifications,created_at,updated_at FROM products")
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
