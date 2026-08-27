package handlers

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"

	"github.com/cutmax/cutmax-backend/internal/config"
	"github.com/cutmax/cutmax-backend/internal/db"
	"github.com/cutmax/cutmax-backend/internal/middleware"
	"github.com/cutmax/cutmax-backend/internal/util"
)

// ===== Public Subscribe =====

func HandleSubscribe(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Email string `json:"email"`
	}
	if err := util.Decode(r, &input); err != nil || input.Email == "" {
		util.JsonErr(w, 400, "Valid email required")
		return
	}
	db.Pool.Exec(r.Context(), "INSERT INTO subscribers (email,ip,created_at) VALUES ($1,$2,NOW()) ON CONFLICT (email) DO NOTHING", input.Email, middleware.ClientIP(r))
	util.JsonOK(w, 200, map[string]interface{}{"message": "Subscribed"})
}

// ===== Public Auth =====

func HandleRegister(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Name, Email, Password, Phone string
		Company                      *string
	}
	if err := util.Decode(r, &input); err != nil || input.Name == "" || input.Email == "" || input.Phone == "" {
		util.JsonErr(w, 400, "Name, email, phone and password required")
		return
	}
	if len(input.Password) < 10 {
		util.JsonErr(w, 422, "Password must be at least 10 characters")
		return
	}
	hash, _ := bcrypt.GenerateFromPassword([]byte(input.Password), 12)
	cid := uuid.New().String()
	_, err := db.Pool.Exec(r.Context(),
		`INSERT INTO customers (id,name,email,phone,company,password_hash,created_at) VALUES ($1,$2,$3,$4,$5,$6,NOW())`,
		cid, input.Name, input.Email, input.Phone, input.Company, string(hash),
	)
	if err != nil {
		util.JsonErr(w, 409, "An account with this email already exists")
		return
	}
	token, exp, _ := middleware.SignJWT(map[string]interface{}{"sub": cid, "email": input.Email, "name": input.Name}, config.Cfg.CustomerJWTSecret, 7*24*time.Hour)
	middleware.SetCookie(w, "cutmax_customer", token, exp)
	util.JsonOK(w, 201, map[string]interface{}{
		"customer": map[string]interface{}{"id": cid, "name": input.Name, "email": input.Email, "phone": input.Phone},
	})
}

func HandleLogin(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Email, Password string
	}
	if err := util.Decode(r, &input); err != nil || input.Email == "" || input.Password == "" {
		util.JsonErr(w, 400, "Email and password required")
		return
	}

	var cust struct {
		ID, Name, Email, Phone, Hash string
		FailedAttempts               int
		LockedUntil                  *time.Time
	}
	err := db.Pool.QueryRow(r.Context(),
		"SELECT id,name,email,phone,password_hash,failed_attempts,locked_until FROM customers WHERE email=$1", input.Email,
	).Scan(&cust.ID, &cust.Name, &cust.Email, &cust.Phone, &cust.Hash, &cust.FailedAttempts, &cust.LockedUntil)
	if err != nil {
		bcrypt.CompareHashAndPassword([]byte("$2a$12$DummyHashForTimingSafety00000000000000000000"), []byte(input.Password))
		util.JsonErr(w, 401, "Invalid email or password")
		return
	}
	if cust.LockedUntil != nil && cust.LockedUntil.After(time.Now()) {
		util.JsonErr(w, 423, "Account temporarily locked")
		return
	}
	if err := bcrypt.CompareHashAndPassword([]byte(cust.Hash), []byte(input.Password)); err != nil {
		db.Pool.Exec(r.Context(), "UPDATE customers SET failed_attempts=failed_attempts+1, locked_until=CASE WHEN failed_attempts+1>=5 THEN NOW()+'15 minutes'::interval ELSE locked_until END WHERE id=$1", cust.ID)
		util.JsonErr(w, 401, "Invalid email or password")
		return
	}
	db.Pool.Exec(r.Context(), "UPDATE customers SET failed_attempts=0, locked_until=NULL, last_login=NOW() WHERE id=$1", cust.ID)
	token, exp, _ := middleware.SignJWT(map[string]interface{}{"sub": cust.ID, "email": cust.Email, "name": cust.Name}, config.Cfg.CustomerJWTSecret, 7*24*time.Hour)
	middleware.SetCookie(w, "cutmax_customer", token, exp)
	util.JsonOK(w, 200, map[string]interface{}{
		"customer": map[string]interface{}{"id": cust.ID, "name": cust.Name, "email": cust.Email, "phone": cust.Phone},
	})
}

func HandleLogout(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{Name: "cutmax_customer", Value: "", Path: "/", MaxAge: -1, HttpOnly: true, Secure: true, SameSite: http.SameSiteNoneMode})
	util.JsonOK(w, 200, map[string]interface{}{"message": "Logged out"})
}

func HandleMe(w http.ResponseWriter, r *http.Request) {
	cid, _ := r.Context().Value(middleware.CustIDKey).(string)
	if cid == "" {
		util.JsonOK(w, 200, map[string]interface{}{"authenticated": false, "customer": nil})
		return
	}
	var name, email, phone, company string
	if err := db.Pool.QueryRow(r.Context(), "SELECT name,email,phone,COALESCE(company,'') FROM customers WHERE id=$1", cid).Scan(&name, &email, &phone, &company); err != nil {
		util.JsonOK(w, 200, map[string]interface{}{"authenticated": false, "customer": nil})
		return
	}
	util.JsonOK(w, 200, map[string]interface{}{"authenticated": true, "customer": map[string]interface{}{"id": cid, "name": name, "email": email, "phone": phone, "company": company}})
}

func HandleMyEnquiries(w http.ResponseWriter, r *http.Request) {
	cid, _ := r.Context().Value(middleware.CustIDKey).(string)
	cemail, _ := r.Context().Value(middleware.CustEmailKey).(string)
	if cid == "" {
		util.JsonErr(w, 401, "Authentication required")
		return
	}
	rows, _ := db.Pool.Query(r.Context(),
		`SELECT id,reference,customer_name,company,phone,email,gstin,shipping_method,payment_preference,message,items_json,subtotal,gst_rate,gst_amount,grand_total,status,created_at,updated_at
         FROM enquiries WHERE customer_id=$1 OR email=$2 ORDER BY created_at DESC`, cid, cemail)
	defer rows.Close()
	enqs := []map[string]interface{}{}
	for rows.Next() {
		var e struct {
			ID, Reference, CustomerName, Phone, Status                 string
			Company, GSTIN, ShippingMethod, PaymentPreference, Message *string
			Email                                                      *string
			ItemsJSON                                                  []byte
			Subtotal, GSTRate, GSTAmount, GrandTotal                   float64
			CreatedAt, UpdatedAt                                       time.Time
		}
		rows.Scan(&e.ID, &e.Reference, &e.CustomerName, &e.Company, &e.Phone, &e.Email, &e.GSTIN, &e.ShippingMethod, &e.PaymentPreference, &e.Message, &e.ItemsJSON, &e.Subtotal, &e.GSTRate, &e.GSTAmount, &e.GrandTotal, &e.Status, &e.CreatedAt, &e.UpdatedAt)
		enqs = append(enqs, map[string]interface{}{
			"id": e.ID, "reference": e.Reference, "customerName": e.CustomerName, "company": e.Company,
			"phone": e.Phone, "email": e.Email, "gstin": e.GSTIN, "shippingMethod": e.ShippingMethod,
			"paymentPreference": e.PaymentPreference, "message": e.Message, "items": json.RawMessage(e.ItemsJSON),
			"subtotal": e.Subtotal, "gstRate": e.GSTRate, "gstAmount": e.GSTAmount, "grandTotal": e.GrandTotal,
			"status": e.Status, "createdAt": e.CreatedAt, "updatedAt": e.UpdatedAt,
		})
	}
	util.JsonOK(w, 200, map[string]interface{}{"enquiries": enqs})
}
