package handlers

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/cutmax/cutmax-backend/internal/db"
	"github.com/cutmax/cutmax-backend/internal/util"
)

// ===== Admin Price Tiers =====

func HandleAdminTiers(w http.ResponseWriter, r *http.Request) {
	if r.Method == "POST" {
		var input struct {
			Label    string  `json:"label"`
			MinQty   int     `json:"minQty"`
			Discount float64 `json:"discountPercent"`
		}
		if err := util.Decode(r, &input); err != nil || input.Label == "" || input.MinQty < 1 {
			util.JsonErr(w, 400, "Label and minQty required")
			return
		}
		var tid string
		db.Pool.QueryRow(r.Context(),
			`INSERT INTO price_tiers (label,min_qty,discount_percent,active,created_at,updated_at) VALUES ($1,$2,$3,true,NOW(),NOW()) RETURNING id`,
			input.Label, input.MinQty, input.Discount,
		).Scan(&tid)
		util.JsonOK(w, 201, map[string]interface{}{"tier": map[string]interface{}{"id": tid}})
		return
	}
	rows, _ := db.Pool.Query(r.Context(), "SELECT id,label,min_qty,discount_percent,active,created_at,updated_at FROM price_tiers ORDER BY min_qty ASC")
	defer rows.Close()
	tiers := []db.PriceTierRow{}
	for rows.Next() {
		var t db.PriceTierRow
		rows.Scan(&t.ID, &t.Label, &t.MinQty, &t.DiscountPercent, &t.Active, &t.CreatedAt, &t.UpdatedAt)
		tiers = append(tiers, t)
	}
	util.JsonOK(w, 200, map[string]interface{}{"tiers": tiers})
}

func HandleAdminTier(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if r.Method == "PUT" {
		var input struct {
			Label    string
			MinQty   int
			Discount float64
			Active   bool
		}
		util.Decode(r, &input)
		db.Pool.Exec(r.Context(), "UPDATE price_tiers SET label=$1,min_qty=$2,discount_percent=$3,active=$4,updated_at=NOW() WHERE id=$5", input.Label, input.MinQty, input.Discount, input.Active, id)
		util.JsonOK(w, 200, map[string]interface{}{"message": "Tier updated"})
		return
	}
	if r.Method == "DELETE" {
		db.Pool.Exec(r.Context(), "DELETE FROM price_tiers WHERE id=$1", id)
		util.JsonOK(w, 200, map[string]interface{}{"message": "Tier deleted"})
		return
	}
	util.JsonErr(w, 405, "Method not allowed")
}
