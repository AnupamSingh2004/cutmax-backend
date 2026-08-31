package db

import (
	"context"
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
	ID          string    `json:"id"`
	SKU         string    `json:"sku"`
	Name        string    `json:"name"`
	Category    string    `json:"category"`
	SubCategory string    `json:"subCategory"`
	Brand       string    `json:"brand"`
	Description string    `json:"description"`
	Price       float64   `json:"price"`
	Stock       int       `json:"stock"`
	Unit        string    `json:"unit"`
	ImageURL    *string   `json:"imageUrl"`
	ImageType   string    `json:"imageType"`
	Featured    bool      `json:"featured"`
	Active      bool      `json:"active"`
	SortOrder   int       `json:"sortOrder"`
	Material    *string   `json:"material"`
	CreatedAt   time.Time `json:"createdAt"`
	UpdatedAt   time.Time `json:"updatedAt"`
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

var scanProduct = func(row pgx.Row, p *ProductRow) error {
	return row.Scan(&p.ID, &p.SKU, &p.Name, &p.Category, &p.SubCategory, &p.Brand, &p.Description, &p.Price, &p.Stock, &p.Unit, &p.ImageURL, &p.ImageType, &p.Featured, &p.Active, &p.SortOrder, &p.Material, &p.CreatedAt, &p.UpdatedAt)
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
		rows.Scan(&p.ID, &p.SKU, &p.Name, &p.Category, &p.SubCategory, &p.Brand, &p.Description, &p.Price, &p.Stock, &p.Unit, &p.ImageURL, &p.ImageType, &p.Featured, &p.Active, &p.SortOrder, &p.Material, &p.CreatedAt, &p.UpdatedAt)
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
		`SELECT id,sku,name,category,sub_category,brand,description,price,stock,unit,image_url,image_type,featured,active,sort_order,material,created_at,updated_at
         FROM products WHERE active=true AND sub_category=$1 AND id!=$2 LIMIT 6`, subCategory, excludeID)
	defer rows.Close()
	var products []ProductRow
	for rows.Next() {
		var p ProductRow
		rows.Scan(&p.ID, &p.SKU, &p.Name, &p.Category, &p.SubCategory, &p.Brand, &p.Description, &p.Price, &p.Stock, &p.Unit, &p.ImageURL, &p.ImageType, &p.Featured, &p.Active, &p.SortOrder, &p.Material, &p.CreatedAt, &p.UpdatedAt)
		products = append(products, p)
	}
	return products
}

func LoadActiveProducts(ctx context.Context) []ProductRow {
	return QueryProducts(ctx, "SELECT id,sku,name,category,sub_category,brand,description,price,stock,unit,image_url,image_type,featured,active,sort_order,material,created_at,updated_at FROM products WHERE active=true")
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
