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
	err := db.Pool.QueryRow(r.Context(),
		"SELECT id,name,email,password_hash,role,failed_attempts,locked_until FROM admin_users WHERE email=$1", input.Email,
	).Scan(&admin.ID, &admin.Name, &admin.Email, &admin.Hash, &admin.Role, &admin.FailedAttempts, &admin.LockedUntil)
	if err != nil {
		bcrypt.CompareHashAndPassword([]byte("$2a$12$DummyHashForTimingSafety00000000000000000000"), []byte(input.Password))
		util.JsonErr(w, 401, "Invalid email or password")
		return
	}
	if admin.LockedUntil != nil && admin.LockedUntil.After(time.Now()) {
		util.JsonErr(w, 423, "Account temporarily locked")
		return
	}
	if err := bcrypt.CompareHashAndPassword([]byte(admin.Hash), []byte(input.Password)); err != nil {
		db.Pool.Exec(r.Context(), "UPDATE admin_users SET failed_attempts=failed_attempts+1, locked_until=CASE WHEN failed_attempts+1>=5 THEN NOW()+'15 minutes'::interval ELSE locked_until END WHERE id=$1", admin.ID)
		util.JsonErr(w, 401, "Invalid email or password")
		return
	}
	db.Pool.Exec(r.Context(), "UPDATE admin_users SET failed_attempts=0, locked_until=NULL, last_login=NOW() WHERE id=$1", admin.ID)
	token, exp, _ := middleware.SignJWT(map[string]interface{}{"sub": admin.ID, "email": admin.Email, "name": admin.Name, "role": admin.Role}, config.Cfg.AdminJWTSecret, 30*time.Minute)
	middleware.SetCookie(w, "cutmax_admin", token, exp)
	util.JsonOK(w, 200, map[string]interface{}{
		"admin": map[string]interface{}{"id": admin.ID, "email": admin.Email, "name": admin.Name, "role": admin.Role},
	})
}

func HandleAdminLogout(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{Name: "cutmax_admin", Value: "", Path: "/", MaxAge: -1, HttpOnly: true, Secure: true, SameSite: http.SameSiteNoneMode})
	util.JsonOK(w, 200, map[string]interface{}{"message": "Logged out"})
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
