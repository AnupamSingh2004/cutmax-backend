package handlers

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/cutmax/cutmax-backend/internal/db"
	"github.com/cutmax/cutmax-backend/internal/util"
)

// ===== Admin Enquiries =====

// nonNullItems guards against an enquiry whose items_json was stored as the
// literal JSON "null" (e.g. an enquiry submitted with an empty cart) --
// returning that as-is crashes the frontend's enquiry.items.map(...).
func nonNullItems(raw []byte) json.RawMessage {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || string(trimmed) == "null" {
		return json.RawMessage("[]")
	}
	return json.RawMessage(raw)
}

func HandleAdminEnquiries(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	where := "1=1"
	args := []interface{}{}
	idx := 1
	if v := q.Get("status"); v != "" {
		where += fmt.Sprintf(" AND e.status=$%d", idx)
		args = append(args, v)
		idx++
	}
	if v := q.Get("q"); v != "" {
		where += fmt.Sprintf(" AND (e.reference ILIKE '%%'||$%d||'%%' OR e.customer_name ILIKE '%%'||$%d||'%%' OR e.phone ILIKE '%%'||$%d||'%%')", idx, idx, idx)
		args = append(args, v)
		idx++
	}
	page := util.Atoi(q.Get("page"), 1)
	perPage := util.Atoi(q.Get("per_page"), 24)
	offset := (page - 1) * perPage

	var total int
	db.Pool.QueryRow(r.Context(), "SELECT COUNT(*) FROM enquiries e WHERE "+where, args...).Scan(&total)
	query := fmt.Sprintf(`SELECT id,reference,customer_name,company,phone,email,gstin,shipping_method,payment_preference,message,items_json,subtotal,gst_rate,gst_amount,grand_total,status,created_at,updated_at FROM enquiries e WHERE %s ORDER BY e.created_at DESC LIMIT $%d OFFSET $%d`, where, idx, idx+1)
	args = append(args, perPage, offset)
	rows, _ := db.Pool.Query(r.Context(), query, args...)
	defer rows.Close()
	enqs := []map[string]interface{}{}
	for rows.Next() {
		var e struct {
			ID, Reference, CustomerName, Phone, Status                        string
			Company, GSTIN, ShippingMethod, PaymentPreference, Message, Email *string
			ItemsJSON                                                         []byte
			Subtotal, GSTRate, GSTAmount, GrandTotal                          float64
			CreatedAt, UpdatedAt                                              time.Time
		}
		rows.Scan(&e.ID, &e.Reference, &e.CustomerName, &e.Company, &e.Phone, &e.Email, &e.GSTIN, &e.ShippingMethod, &e.PaymentPreference, &e.Message, &e.ItemsJSON, &e.Subtotal, &e.GSTRate, &e.GSTAmount, &e.GrandTotal, &e.Status, &e.CreatedAt, &e.UpdatedAt)
		enqs = append(enqs, map[string]interface{}{
			"id": e.ID, "reference": e.Reference, "customerName": e.CustomerName, "company": e.Company,
			"phone": e.Phone, "email": e.Email, "gstin": e.GSTIN, "shippingMethod": e.ShippingMethod,
			"paymentPreference": e.PaymentPreference, "message": e.Message, "items": nonNullItems(e.ItemsJSON),
			"subtotal": e.Subtotal, "gstRate": e.GSTRate, "gstAmount": e.GSTAmount, "grandTotal": e.GrandTotal,
			"status": e.Status, "createdAt": e.CreatedAt, "updatedAt": e.UpdatedAt,
		})
	}
	util.JsonOK(w, 200, map[string]interface{}{"enquiries": enqs, "total": total, "page": page, "per_page": perPage})
}

func HandleAdminEnquiry(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if r.Method == "PUT" {
		var input struct {
			Status string `json:"status"`
		}
		util.Decode(r, &input)
		db.Pool.Exec(r.Context(), "UPDATE enquiries SET status=$1,updated_at=NOW() WHERE id=$2", input.Status, id)
		util.JsonOK(w, 200, map[string]interface{}{"message": "Status updated"})
		return
	}
	if r.Method == "DELETE" {
		db.Pool.Exec(r.Context(), "DELETE FROM enquiry_items WHERE enquiry_id=$1", id)
		db.Pool.Exec(r.Context(), "DELETE FROM enquiries WHERE id=$1", id)
		util.JsonOK(w, 200, map[string]interface{}{"message": "Enquiry deleted"})
		return
	}
	// GET
	var e struct {
		ID, Reference, CustomerName, Phone, Status                        string
		Company, GSTIN, ShippingMethod, PaymentPreference, Message, Email *string
		ItemsJSON                                                         []byte
		Subtotal, GSTRate, GSTAmount, GrandTotal                          float64
		CreatedAt, UpdatedAt                                              time.Time
	}
	err := db.Pool.QueryRow(r.Context(),
		`SELECT id,reference,customer_name,company,phone,email,gstin,shipping_method,payment_preference,message,items_json,subtotal,gst_rate,gst_amount,grand_total,status,created_at,updated_at FROM enquiries WHERE id=$1`, id,
	).Scan(&e.ID, &e.Reference, &e.CustomerName, &e.Company, &e.Phone, &e.Email, &e.GSTIN, &e.ShippingMethod, &e.PaymentPreference, &e.Message, &e.ItemsJSON, &e.Subtotal, &e.GSTRate, &e.GSTAmount, &e.GrandTotal, &e.Status, &e.CreatedAt, &e.UpdatedAt)
	if err != nil {
		util.JsonErr(w, 404, "Enquiry not found")
		return
	}
	util.JsonOK(w, 200, map[string]interface{}{
		"enquiry": map[string]interface{}{
			"id": e.ID, "reference": e.Reference, "customerName": e.CustomerName, "company": e.Company,
			"phone": e.Phone, "email": e.Email, "gstin": e.GSTIN, "shippingMethod": e.ShippingMethod,
			"paymentPreference": e.PaymentPreference, "message": e.Message, "items": nonNullItems(e.ItemsJSON),
			"subtotal": e.Subtotal, "gstRate": e.GSTRate, "gstAmount": e.GSTAmount, "grandTotal": e.GrandTotal,
			"status": e.Status, "createdAt": e.CreatedAt, "updatedAt": e.UpdatedAt,
		},
	})
}
