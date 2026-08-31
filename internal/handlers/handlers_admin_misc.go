package handlers

import (
	"net/http"
	"time"

	"github.com/cutmax/cutmax-backend/internal/db"
	"github.com/cutmax/cutmax-backend/internal/util"
)

// ===== Admin Settings =====

func HandleAdminSettings(w http.ResponseWriter, r *http.Request) {
	if r.Method == "PUT" {
		var input struct {
			Values map[string]string `json:"values"`
		}
		util.Decode(r, &input)
		tx, _ := db.Pool.Begin(r.Context())
		defer tx.Rollback(r.Context())
		for k, v := range input.Values {
			tx.Exec(r.Context(), "INSERT INTO settings (key,value,updated_at) VALUES ($1,$2,NOW()) ON CONFLICT (key) DO UPDATE SET value=$2,updated_at=NOW()", k, v)
		}
		tx.Commit(r.Context())
		util.JsonOK(w, 200, map[string]interface{}{"message": "Settings updated"})
		return
	}
	rows, _ := db.Pool.Query(r.Context(), "SELECT key,value FROM settings")
	defer rows.Close()
	settings := map[string]string{}
	for rows.Next() {
		var k, v string
		rows.Scan(&k, &v)
		settings[k] = v
	}
	util.JsonOK(w, 200, map[string]interface{}{"settings": settings})
}

// ===== Admin Stats =====

func HandleAdminStats(w http.ResponseWriter, r *http.Request) {
	var k struct {
		TotalProducts   int `json:"totalProducts"`
		StockUnits      int `json:"stockUnits"`
		StockValue      int `json:"stockValue"`
		TotalEnquiries  int `json:"totalEnquiries"`
		NewEnquiries    int `json:"newEnquiries"`
		LowStockCount   int `json:"lowStockCount"`
		OutOfStockCount int `json:"outOfStockCount"`
	}
	db.Pool.QueryRow(r.Context(), "SELECT COUNT(*) FROM products WHERE active=true").Scan(&k.TotalProducts)
	db.Pool.QueryRow(r.Context(), "SELECT COALESCE(SUM(stock),0) FROM products WHERE active=true").Scan(&k.StockUnits)
	db.Pool.QueryRow(r.Context(), "SELECT COALESCE(SUM(stock*price)::int,0) FROM products WHERE active=true").Scan(&k.StockValue)
	db.Pool.QueryRow(r.Context(), "SELECT COUNT(*) FROM enquiries").Scan(&k.TotalEnquiries)
	db.Pool.QueryRow(r.Context(), "SELECT COUNT(*) FROM enquiries WHERE status='NEW'").Scan(&k.NewEnquiries)

	var lowLimit int
	db.Pool.QueryRow(r.Context(), "SELECT COALESCE(value::int,10) FROM settings WHERE key='low_stock_limit'").Scan(&lowLimit)
	db.Pool.QueryRow(r.Context(), "SELECT COUNT(*) FROM products WHERE active=true AND stock>0 AND stock<=$1", lowLimit).Scan(&k.LowStockCount)
	db.Pool.QueryRow(r.Context(), "SELECT COUNT(*) FROM products WHERE active=true AND stock=0").Scan(&k.OutOfStockCount)

	stockBySubCategory := []map[string]interface{}{}
	rows, _ := db.Pool.Query(r.Context(),
		"SELECT sub_category, COALESCE(SUM(stock),0) FROM products WHERE active=true GROUP BY sub_category ORDER BY 2 DESC LIMIT 12")
	for rows.Next() {
		var sub string
		var stock int
		rows.Scan(&sub, &stock)
		stockBySubCategory = append(stockBySubCategory, map[string]interface{}{"subCategory": sub, "stock": stock})
	}
	rows.Close()

	stockStatus := []map[string]interface{}{}
	rows, _ = db.Pool.Query(r.Context(), `
		SELECT
			CASE WHEN stock=0 THEN 'Out of Stock' WHEN stock<=$1 THEN 'Low Stock' ELSE 'In Stock' END AS status,
			COUNT(*), COALESCE(SUM(stock*price),0)::int
		FROM products WHERE active=true GROUP BY 1`, lowLimit)
	for rows.Next() {
		var status string
		var count, value int
		rows.Scan(&status, &count, &value)
		stockStatus = append(stockStatus, map[string]interface{}{"status": status, "count": count, "value": value})
	}
	rows.Close()

	enquiriesOverTime := []map[string]interface{}{}
	rows, _ = db.Pool.Query(r.Context(), `
		SELECT to_char(d::date,'YYYY-MM-DD'), COUNT(e.id)
		FROM generate_series(CURRENT_DATE - INTERVAL '29 days', CURRENT_DATE, INTERVAL '1 day') d
		LEFT JOIN enquiries e ON e.created_at::date = d::date
		GROUP BY d ORDER BY d`)
	for rows.Next() {
		var date string
		var count int
		rows.Scan(&date, &count)
		enquiriesOverTime = append(enquiriesOverTime, map[string]interface{}{"date": date, "count": count})
	}
	rows.Close()

	enquiryStatusBreakdown := []map[string]interface{}{}
	rows, _ = db.Pool.Query(r.Context(), "SELECT status, COUNT(*) FROM enquiries GROUP BY status")
	for rows.Next() {
		var status string
		var count int
		rows.Scan(&status, &count)
		enquiryStatusBreakdown = append(enquiryStatusBreakdown, map[string]interface{}{"status": status, "count": count})
	}
	rows.Close()

	topProducts := []map[string]interface{}{}
	rows, _ = db.Pool.Query(r.Context(), `
		SELECT sku, name, COUNT(DISTINCT enquiry_id), COALESCE(SUM(qty),0)
		FROM enquiry_items GROUP BY sku, name ORDER BY 3 DESC LIMIT 5`)
	for rows.Next() {
		var sku, name string
		var enquiryCount, totalQty int
		rows.Scan(&sku, &name, &enquiryCount, &totalQty)
		topProducts = append(topProducts, map[string]interface{}{"sku": sku, "name": name, "enquiryCount": enquiryCount, "totalQty": totalQty})
	}
	rows.Close()

	util.JsonOK(w, 200, map[string]interface{}{
		"kpis": k, "stockBySubCategory": stockBySubCategory, "stockStatus": stockStatus,
		"enquiriesOverTime": enquiriesOverTime, "enquiryStatusBreakdown": enquiryStatusBreakdown, "topProducts": topProducts,
	})
}

// ===== Admin Audit =====

func HandleAdminAudit(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	page := util.Atoi(q.Get("page"), 1)
	perPage := util.Atoi(q.Get("per_page"), 50)
	offset := (page - 1) * perPage
	var total int
	db.Pool.QueryRow(r.Context(), "SELECT COUNT(*) FROM audit_log").Scan(&total)
	rows, _ := db.Pool.Query(r.Context(),
		`SELECT a.id,a.action,a.detail,a.status,a.ip,a.user_agent,a.created_at,COALESCE(au.email,'') AS admin_email
         FROM audit_log a LEFT JOIN admin_users au ON a.admin_id=au.id ORDER BY a.created_at DESC LIMIT $1 OFFSET $2`, perPage, offset)
	defer rows.Close()
	logs := []map[string]interface{}{}
	for rows.Next() {
		var l struct {
			ID, Action, Status string
			Detail             *string
			IP, UserAgent      *string
			CreatedAt          time.Time
			AdminEmail         string
		}
		rows.Scan(&l.ID, &l.Action, &l.Detail, &l.Status, &l.IP, &l.UserAgent, &l.CreatedAt, &l.AdminEmail)
		logs = append(logs, map[string]interface{}{
			"id": l.ID, "action": l.Action, "detail": l.Detail, "status": l.Status,
			"ip": l.IP, "userAgent": l.UserAgent, "createdAt": l.CreatedAt, "adminEmail": l.AdminEmail,
		})
	}
	util.JsonOK(w, 200, map[string]interface{}{"logs": logs, "total": total, "page": page, "per_page": perPage})
}
