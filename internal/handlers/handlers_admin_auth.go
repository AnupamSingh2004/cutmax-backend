package handlers

import (
	"net/http"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/cutmax/cutmax-backend/internal/config"
	"github.com/cutmax/cutmax-backend/internal/db"
	"github.com/cutmax/cutmax-backend/internal/middleware"
	"github.com/cutmax/cutmax-backend/internal/util"
)

// ===== Admin Auth =====

func HandleAdminLogin(w http.ResponseWriter, r *http.Request) {
	var input struct{ Email, Password string }
	if err := util.Decode(r, &input); err != nil || input.Email == "" || input.Password == "" {
		util.JsonErr(w, 400, "Email and password required")
		return
	}
	var admin struct {
		ID, Name, Email, Hash, Role string
		FailedAttempts              int
		LockedUntil                 *time.Time
	}
	ip, ua := middleware.ClientIP(r), r.Header.Get("User-Agent")
	err := db.Pool.QueryRow(r.Context(),
		"SELECT id,name,email,password_hash,role,failed_attempts,locked_until FROM admin_users WHERE email=$1", input.Email,
	).Scan(&admin.ID, &admin.Name, &admin.Email, &admin.Hash, &admin.Role, &admin.FailedAttempts, &admin.LockedUntil)
	if err != nil {
		bcrypt.CompareHashAndPassword([]byte("$2a$12$DummyHashForTimingSafety00000000000000000000"), []byte(input.Password))
		db.WriteAudit(r.Context(), nil, "admin_login", input.Email, "FAILED", ip, ua)
		util.JsonErr(w, 401, "Invalid email or password")
		return
	}
	if admin.LockedUntil != nil && admin.LockedUntil.After(time.Now()) {
		db.WriteAudit(r.Context(), &admin.ID, "admin_login", admin.Email, "LOCKED", ip, ua)
		util.JsonErr(w, 423, "Account temporarily locked")
		return
	}
	if err := bcrypt.CompareHashAndPassword([]byte(admin.Hash), []byte(input.Password)); err != nil {
		db.Pool.Exec(r.Context(), "UPDATE admin_users SET failed_attempts=failed_attempts+1, locked_until=CASE WHEN failed_attempts+1>=5 THEN NOW()+'15 minutes'::interval ELSE locked_until END WHERE id=$1", admin.ID)
		db.WriteAudit(r.Context(), &admin.ID, "admin_login", admin.Email, "FAILED", ip, ua)
		util.JsonErr(w, 401, "Invalid email or password")
		return
	}
	db.Pool.Exec(r.Context(), "UPDATE admin_users SET failed_attempts=0, locked_until=NULL, last_login=NOW() WHERE id=$1", admin.ID)
	db.WriteAudit(r.Context(), &admin.ID, "admin_login", admin.Email, "OK", ip, ua)
	token, exp, _ := middleware.SignJWT(map[string]interface{}{"sub": admin.ID, "email": admin.Email, "name": admin.Name, "role": admin.Role}, config.Cfg.AdminJWTSecret, 8*time.Hour)
	middleware.SetCookie(w, "cutmax_admin", token, exp)
	util.JsonOK(w, 200, map[string]interface{}{
		"admin": map[string]interface{}{"id": admin.ID, "email": admin.Email, "name": admin.Name, "role": admin.Role},
	})
}

func HandleAdminLogout(w http.ResponseWriter, r *http.Request) {
	// Not gated behind RequireAdmin (logout must always succeed even with a
	// stale/expired cookie), so the JWT is parsed directly here, best-effort,
	// just to attribute the audit entry.
	if cookie, err := r.Cookie("cutmax_admin"); err == nil {
		if claims, err := middleware.VerifyJWT(cookie.Value, config.Cfg.AdminJWTSecret); err == nil {
			id, _ := claims["sub"].(string)
			email, _ := claims["email"].(string)
			if id != "" {
				db.WriteAudit(r.Context(), &id, "admin_logout", email, "OK", middleware.ClientIP(r), r.Header.Get("User-Agent"))
			}
		}
	}
	http.SetCookie(w, &http.Cookie{Name: "cutmax_admin", Value: "", Path: "/", MaxAge: -1, HttpOnly: true, Secure: true, SameSite: http.SameSiteNoneMode})
	util.JsonOK(w, 200, map[string]interface{}{"message": "Logged out"})
}

func HandleAdminChangePassword(w http.ResponseWriter, r *http.Request) {
	var input struct{ CurrentPassword, NewPassword, ConfirmPassword string }
	if err := util.Decode(r, &input); err != nil || input.CurrentPassword == "" || input.NewPassword == "" || input.ConfirmPassword == "" {
		util.JsonErr(w, 400, "Current password, new password and confirmation are required")
		return
	}
	if input.NewPassword != input.ConfirmPassword {
		util.JsonErr(w, 400, "New password and confirmation do not match")
		return
	}
	if len(input.NewPassword) < 10 {
		util.JsonErr(w, 400, "New password must be at least 10 characters")
		return
	}
	if input.NewPassword == input.CurrentPassword {
		util.JsonErr(w, 400, "New password must be different from the current password")
		return
	}

	id, _ := r.Context().Value(middleware.AdminIDKey).(string)
	email, _ := r.Context().Value(middleware.AdminEmailKey).(string)
	ip, ua := middleware.ClientIP(r), r.Header.Get("User-Agent")
	var hash string
	if err := db.Pool.QueryRow(r.Context(), "SELECT password_hash FROM admin_users WHERE id=$1", id).Scan(&hash); err != nil {
		util.JsonErr(w, 404, "Admin not found")
		return
	}
	if err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(input.CurrentPassword)); err != nil {
		db.WriteAudit(r.Context(), &id, "admin_password_change", email, "FAILED", ip, ua)
		util.JsonErr(w, 401, "Current password is incorrect")
		return
	}

	newHash, err := bcrypt.GenerateFromPassword([]byte(input.NewPassword), 12)
	if err != nil {
		util.JsonErr(w, 500, "Could not set new password")
		return
	}
	if _, err := db.Pool.Exec(r.Context(), "UPDATE admin_users SET password_hash=$1 WHERE id=$2", string(newHash), id); err != nil {
		util.JsonErr(w, 500, "Could not update password")
		return
	}
	db.WriteAudit(r.Context(), &id, "admin_password_change", email, "OK", ip, ua)

	// Force re-login with the new password rather than keeping the current session alive.
	http.SetCookie(w, &http.Cookie{Name: "cutmax_admin", Value: "", Path: "/", MaxAge: -1, HttpOnly: true, Secure: true, SameSite: http.SameSiteNoneMode})
	util.JsonOK(w, 200, map[string]interface{}{"message": "Password updated. Please log in again."})
}

func HandleAdminMe(w http.ResponseWriter, r *http.Request) {
	id, _ := r.Context().Value(middleware.AdminIDKey).(string)
	email, _ := r.Context().Value(middleware.AdminEmailKey).(string)
	name, _ := r.Context().Value(middleware.AdminNameKey).(string)
	role, _ := r.Context().Value(middleware.AdminRoleKey).(string)
	util.JsonOK(w, 200, map[string]interface{}{
		"admin": map[string]interface{}{"id": id, "email": email, "name": name, "role": role},
	})
}
