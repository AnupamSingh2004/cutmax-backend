package handlers

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"

	"github.com/google/uuid"

	"github.com/cutmax/cutmax-backend/internal/db"
	"github.com/cutmax/cutmax-backend/internal/middleware"
	"github.com/cutmax/cutmax-backend/internal/util"
)

// ===== Public Enquiry =====

func HandleCreateEnquiry(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Name     string  `json:"name"`
		Company  *string `json:"company"`
		Phone    string  `json:"phone"`
		Email    *string `json:"email"`
		GSTIN    *string `json:"gstin"`
		Shipping *string `json:"shipping"`
		Payment  *string `json:"payment"`
		Message  *string `json:"message"`
		Items    []struct {
			SKU       string  `json:"sku"`
			Name      string  `json:"name"`
			Category  string  `json:"category"`
			Qty       int     `json:"qty"`
			UnitPrice float64 `json:"unitPrice"`
			LineTotal float64 `json:"lineTotal"`
		} `json:"items"`
		Subtotal, GSTRate, GSTAmount, GrandTotal float64
	}
	if err := util.Decode(r, &input); err != nil || input.Name == "" || input.Phone == "" || len(input.Items) == 0 {
		util.JsonErr(w, 422, "Name, phone and at least one item are required")
		return
	}

	ref := util.GenRef()
	ip := middleware.ClientIP(r)
	ua := r.UserAgent()
	custID, _ := r.Context().Value(middleware.CustIDKey).(string)
	itemsJSON, _ := json.Marshal(input.Items)

	tx, err := db.Pool.Begin(r.Context())
	if err != nil {
		util.JsonErr(w, 500, "Transaction error")
		return
	}
	defer tx.Rollback(r.Context())

	var eid string
	eid = uuid.New().String()
	_, err = tx.Exec(r.Context(),
		`INSERT INTO enquiries (id,customer_id,reference,customer_name,company,phone,email,gstin,shipping_method,payment_preference,message,items_json,subtotal,gst_rate,gst_amount,grand_total,ip,user_agent,created_at,updated_at)
         VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12::jsonb,$13,$14,$15,$16,$17,$18,NOW(),NOW())`,
		eid, util.NullStr(custID), ref, input.Name, input.Company, input.Phone, input.Email, input.GSTIN,
		input.Shipping, input.Payment, input.Message, string(itemsJSON),
		input.Subtotal, input.GSTRate, input.GSTAmount, input.GrandTotal, ip, ua,
	)
	if err != nil {
		log.Printf("[enquiry] INSERT error: %v", err)
		util.JsonErr(w, 500, "Failed to create enquiry")
		return
	}

	for _, item := range input.Items {
		_, err := tx.Exec(r.Context(),
			`INSERT INTO enquiry_items (id,enquiry_id,sku,name,category,qty,unit_price,line_total) VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`,
			uuid.New().String(), eid, item.SKU, item.Name, item.Category, item.Qty, item.UnitPrice, item.LineTotal,
		)
		if err != nil {
			log.Printf("[enquiry] items INSERT error: %v", err)
			util.JsonErr(w, 500, "Failed to save enquiry items")
			return
		}
	}
	if err := tx.Commit(r.Context()); err != nil {
		log.Printf("[enquiry] commit error: %v", err)
		util.JsonErr(w, 500, "Failed to save enquiry")
		return
	}

	company := input.Name
	if input.Company != nil && *input.Company != "" {
		company = *input.Company
	}
	db.WriteAudit(r.Context(), nil, "enquiry_submitted", fmt.Sprintf("%s — %s", ref, company), "OK", ip, ua)

	util.JsonOK(w, 201, map[string]interface{}{"reference": ref, "message": "Enquiry submitted successfully"})
}
