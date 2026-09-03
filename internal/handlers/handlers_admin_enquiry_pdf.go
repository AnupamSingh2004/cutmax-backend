package handlers

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-pdf/fpdf"

	"github.com/cutmax/cutmax-backend/internal/db"
	"github.com/cutmax/cutmax-backend/internal/util"
)

// parseItems mirrors nonNullItems' tolerance for a missing/malformed
// items_json column but returns the typed slice a PDF table needs to iterate,
// rather than a re-marshaled json.RawMessage for an HTTP response body.
func parseItems(raw []byte) []enquiryItemOut {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || string(trimmed) == "null" {
		return nil
	}
	var items []enquiryItemOut
	if err := json.Unmarshal(trimmed, &items); err != nil {
		return nil
	}
	return items
}

func str(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func HandleAdminEnquiryPDF(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

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
	items := parseItems(e.ItemsJSON)
	settings := db.LoadPublicSettings(r.Context())
	companyName := settings["company_name"].(string)
	if companyName == "" {
		companyName = "Cutmax Technologies"
	}
	companyAddress := settings["company_address"].(string)
	companyPhone := settings["company_phone"].(string)

	pdf := fpdf.New("P", "mm", "A4", "")
	pdf.SetMargins(15, 15, 15)
	pdf.SetAutoPageBreak(true, 20)
	pdf.AddPage()

	pdf.SetFont("Helvetica", "B", 18)
	pdf.CellFormat(0, 9, companyName, "", 1, "L", false, 0, "")
	pdf.SetFont("Helvetica", "", 10)
	if companyAddress != "" {
		pdf.CellFormat(0, 5, companyAddress, "", 1, "L", false, 0, "")
	}
	if companyPhone != "" {
		pdf.CellFormat(0, 5, "Phone: "+companyPhone, "", 1, "L", false, 0, "")
	}
	pdf.Ln(4)

	pdf.SetFont("Helvetica", "B", 14)
	pdf.CellFormat(0, 8, "QUOTATION", "", 1, "L", false, 0, "")
	pdf.SetFont("Helvetica", "", 10)
	pdf.CellFormat(0, 6, fmt.Sprintf("Reference: %s", e.Reference), "", 1, "L", false, 0, "")
	pdf.CellFormat(0, 6, fmt.Sprintf("Date: %s", e.CreatedAt.Format("02 Jan 2006")), "", 1, "L", false, 0, "")
	pdf.CellFormat(0, 6, fmt.Sprintf("Status: %s", e.Status), "", 1, "L", false, 0, "")
	pdf.Ln(4)

	pdf.SetFont("Helvetica", "B", 11)
	pdf.CellFormat(0, 6, "Customer Details", "", 1, "L", false, 0, "")
	pdf.SetFont("Helvetica", "", 10)
	pdf.CellFormat(0, 5.5, "Name: "+e.CustomerName, "", 1, "L", false, 0, "")
	if c := str(e.Company); c != "" {
		pdf.CellFormat(0, 5.5, "Company: "+c, "", 1, "L", false, 0, "")
	}
	pdf.CellFormat(0, 5.5, "Phone: "+e.Phone, "", 1, "L", false, 0, "")
	if em := str(e.Email); em != "" {
		pdf.CellFormat(0, 5.5, "Email: "+em, "", 1, "L", false, 0, "")
	}
	if g := str(e.GSTIN); g != "" {
		pdf.CellFormat(0, 5.5, "GSTIN: "+g, "", 1, "L", false, 0, "")
	}
	if sm := str(e.ShippingMethod); sm != "" {
		pdf.CellFormat(0, 5.5, "Shipping: "+sm, "", 1, "L", false, 0, "")
	}
	if pp := str(e.PaymentPreference); pp != "" {
		pdf.CellFormat(0, 5.5, "Payment Preference: "+pp, "", 1, "L", false, 0, "")
	}
	pdf.Ln(4)

	colWidths := []float64{25, 60, 30, 15, 25, 25}
	headers := []string{"SKU", "Name", "Category", "Qty", "Unit Price", "Line Total"}
	pdf.SetFont("Helvetica", "B", 9)
	pdf.SetFillColor(230, 230, 230)
	for i, h := range headers {
		pdf.CellFormat(colWidths[i], 7, h, "1", 0, "L", true, 0, "")
	}
	pdf.Ln(-1)

	pdf.SetFont("Helvetica", "", 9)
	for _, it := range items {
		pdf.CellFormat(colWidths[0], 6.5, it.SKU, "1", 0, "L", false, 0, "")
		pdf.CellFormat(colWidths[1], 6.5, it.Name, "1", 0, "L", false, 0, "")
		pdf.CellFormat(colWidths[2], 6.5, it.Category, "1", 0, "L", false, 0, "")
		pdf.CellFormat(colWidths[3], 6.5, fmt.Sprintf("%d", it.Qty), "1", 0, "R", false, 0, "")
		pdf.CellFormat(colWidths[4], 6.5, fmt.Sprintf("%.2f", it.UnitPrice), "1", 0, "R", false, 0, "")
		pdf.CellFormat(colWidths[5], 6.5, fmt.Sprintf("%.2f", it.LineTotal), "1", 1, "R", false, 0, "")
	}
	pdf.Ln(4)

	labelWidth := colWidths[0] + colWidths[1] + colWidths[2] + colWidths[3] + colWidths[4]
	pdf.SetFont("Helvetica", "", 10)
	pdf.CellFormat(labelWidth, 6.5, "Subtotal", "", 0, "R", false, 0, "")
	pdf.CellFormat(colWidths[5], 6.5, fmt.Sprintf("%.2f", e.Subtotal), "", 1, "R", false, 0, "")
	pdf.CellFormat(labelWidth, 6.5, fmt.Sprintf("GST (%.0f%%)", e.GSTRate), "", 0, "R", false, 0, "")
	pdf.CellFormat(colWidths[5], 6.5, fmt.Sprintf("%.2f", e.GSTAmount), "", 1, "R", false, 0, "")
	pdf.SetFont("Helvetica", "B", 11)
	pdf.CellFormat(labelWidth, 8, "Grand Total", "", 0, "R", false, 0, "")
	pdf.CellFormat(colWidths[5], 8, fmt.Sprintf("Rs. %.2f", e.GrandTotal), "", 1, "R", false, 0, "")

	if m := str(e.Message); m != "" {
		pdf.Ln(6)
		pdf.SetFont("Helvetica", "B", 11)
		pdf.CellFormat(0, 6, "Notes", "", 1, "L", false, 0, "")
		pdf.SetFont("Helvetica", "", 10)
		pdf.MultiCell(0, 5.5, m, "", "L", false)
	}

	var buf bytes.Buffer
	if err := pdf.Output(&buf); err != nil {
		util.JsonErr(w, 500, "Failed to generate PDF")
		return
	}

	disposition := "inline"
	if r.URL.Query().Get("download") == "1" {
		disposition = "attachment"
	}
	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`%s; filename="quote-%s.pdf"`, disposition, e.Reference))
	w.WriteHeader(200)
	w.Write(buf.Bytes())
}
