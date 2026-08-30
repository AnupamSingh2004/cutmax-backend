package middleware

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/redis/go-redis/v9"

	"github.com/cutmax/cutmax-backend/internal/config"
)

type ctxKey string

const (
	AdminIDKey    ctxKey = "admin_id"
	AdminEmailKey ctxKey = "admin_email"
	AdminNameKey  ctxKey = "admin_name"
	AdminRoleKey  ctxKey = "admin_role"
	CustIDKey     ctxKey = "customer_id"
	CustEmailKey  ctxKey = "customer_email"
	CustNameKey   ctxKey = "customer_name"
)

// --- Security Headers ---
func SecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains; preload")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
		w.Header().Set("X-XSS-Protection", "1; mode=block")
		w.Header().Set("Permissions-Policy", "geolocation=(), microphone=(), camera=(), payment=()")
		next.ServeHTTP(w, r)
	})
}

// --- CORS ---
func CORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin != "" && OriginAllowed(origin) {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Access-Control-Allow-Credentials", "true")
			w.Header().Set("Vary", "Origin")
		}
		w.Header().Set("Access-Control-Allow-Methods", "GET,POST,PUT,PATCH,DELETE,OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, X-CSRF-Token, X-Requested-With, Authorization")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func OriginAllowed(o string) bool {
	for _, allowed := range config.Cfg.AllowedOrigins {
		if allowed == o {
			return true
		}
	}
	return false
}

// --- CSRF ---
const (
	CSRFCookie    = "cutmax_csrf"
	CSRFHeader    = "X-CSRF-Token"
	CSRFMarker    = "X-Requested-With"
	CSRFMarkerVal = "cutmax"
)

var mutating = map[string]bool{"POST": true, "PUT": true, "PATCH": true, "DELETE": true}

func CSRF(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if mutating[r.Method] {
			if !OriginAllowed(r.Header.Get("Origin")) {
				http.Error(w, `{"success":false,"error":"Origin not allowed"}`, http.StatusForbidden)
				return
			}
			if r.Header.Get(CSRFMarker) != CSRFMarkerVal {
				http.Error(w, `{"success":false,"error":"Missing required request header"}`, http.StatusForbidden)
				return
			}
			if cookie, err := r.Cookie(CSRFCookie); err == nil {
				if !TokensMatch(cookie.Value, r.Header.Get(CSRFHeader)) {
					http.Error(w, `{"success":false,"error":"CSRF token mismatch"}`, http.StatusForbidden)
					return
				}
			}
		}
		next.ServeHTTP(w, r)
	})
}

func TokensMatch(a, b string) bool {
	if a == "" || b == "" || len(a) != len(b) {
		return false
	}
	var d byte
	for i := 0; i < len(a); i++ {
		d |= a[i] ^ b[i]
	}
	return d == 0
}

// --- Rate Limiting ---
type RLBucket string

const (
	RLAdminLogin    RLBucket = "admin-login"
	RLCustomerLogin RLBucket = "customer-login"
	RLCustomerReg   RLBucket = "customer-register"
	RLEnquirySubmit RLBucket = "enquiry-submit"
	RLSubscribe     RLBucket = "subscribe"
	RLBulkPrices    RLBucket = "bulk-price-update"
	RLBulkProducts  RLBucket = "bulk-import-products"
	RLBulkImages    RLBucket = "bulk-import-images"
	RLImageUpload   RLBucket = "image-upload"
)

var rlLimits = map[RLBucket][2]int{
	RLAdminLogin:    {10, 300},
	RLCustomerLogin: {10, 300},
	RLCustomerReg:   {5, 600},
	RLEnquirySubmit: {5, 600},
	RLSubscribe:     {3, 600},
	RLBulkPrices:    {30, 600},
	RLBulkProducts:  {5, 600},
	RLBulkImages:    {20, 600},
	RLImageUpload:   {20, 300},
}

func RateLimit(bucket RLBucket) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			limit, window := rlLimits[bucket][0], rlLimits[bucket][1]
			key := "rl:" + string(bucket) + ":" + ClientIP(r)
			ctx := r.Context()
			rdb := redisClient()
			count, _ := rdb.Incr(ctx, key).Result()
			if count == 1 {
				rdb.Expire(ctx, key, time.Duration(window)*time.Second)
			}
			ttl, _ := rdb.TTL(ctx, key).Result()
			secs := int(ttl / time.Second)
			if secs <= 0 {
				secs = window
			}
			if count > int64(limit) {
				w.Header().Set("Retry-After", itoa(secs))
				http.Error(w, `{"success":false,"error":"Too many requests. Please try again later.","retryAfterSeconds":`+itoa(secs)+`}`, http.StatusTooManyRequests)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func itoa(i int) string {
	return strings.TrimRight(strings.TrimRight(time.Unix(0, 0).Add(time.Duration(i)*time.Second).Format("070102150405"), "0"), ".")
}

// --- Auth ---
func RequireAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie("cutmax_admin")
		if err != nil {
			http.Error(w, `{"success":false,"error":"Authentication required"}`, http.StatusUnauthorized)
			return
		}
		claims, err := VerifyJWT(cookie.Value, config.Cfg.AdminJWTSecret)
		if err != nil {
			http.Error(w, `{"success":false,"error":"Invalid session"}`, http.StatusUnauthorized)
			return
		}
		// Rolling 30-min idle timeout
		token, exp, _ := SignJWT(claims, config.Cfg.AdminJWTSecret, 30*time.Minute)
		SetCookie(w, "cutmax_admin", token, exp)

		ctx := context.WithValue(r.Context(), AdminIDKey, claims["sub"])
		ctx = context.WithValue(ctx, AdminEmailKey, claims["email"])
		ctx = context.WithValue(ctx, AdminNameKey, claims["name"])
		ctx = context.WithValue(ctx, AdminRoleKey, claims["role"])
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func OptionalCustomer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if cookie, err := r.Cookie("cutmax_customer"); err == nil {
			if claims, err := VerifyJWT(cookie.Value, config.Cfg.CustomerJWTSecret); err == nil {
				ctx := context.WithValue(r.Context(), CustIDKey, claims["sub"])
				ctx = context.WithValue(ctx, CustEmailKey, claims["email"])
				r = r.WithContext(ctx)
			}
		}
		next.ServeHTTP(w, r)
	})
}

func RequireCustomer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie("cutmax_customer")
		if err != nil {
			http.Error(w, `{"success":false,"error":"Authentication required"}`, http.StatusUnauthorized)
			return
		}
		claims, err := VerifyJWT(cookie.Value, config.Cfg.CustomerJWTSecret)
		if err != nil {
			http.Error(w, `{"success":false,"error":"Invalid session"}`, http.StatusUnauthorized)
			return
		}
		ctx := context.WithValue(r.Context(), CustIDKey, claims["sub"])
		ctx = context.WithValue(ctx, CustEmailKey, claims["email"])
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func ClientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		return strings.TrimSpace(strings.Split(xff, ",")[0])
	}
	if xr := r.Header.Get("X-Real-IP"); xr != "" {
		return xr
	}
	host := r.RemoteAddr
	if i := strings.LastIndex(host, ":"); i != -1 {
		return host[:i]
	}
	return host
}

// --- JWT ---
func SignJWT(claims map[string]interface{}, secret string, exp time.Duration) (string, time.Time, error) {
	expiresAt := time.Now().Add(exp)
	jti := make([]byte, 16)
	rand.Read(jti)
	t := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"sub": claims["sub"], "email": claims["email"], "name": claims["name"],
		"role": claims["role"], "jti": hex.EncodeToString(jti),
		"iat": time.Now().Unix(), "exp": expiresAt.Unix(),
	})
	token, err := t.SignedString([]byte(secret))
	return token, expiresAt, err
}

func VerifyJWT(tokenStr, secret string) (map[string]interface{}, error) {
	t, err := jwt.Parse(tokenStr, func(t *jwt.Token) (interface{}, error) { return []byte(secret), nil }, jwt.WithValidMethods([]string{"HS256"}))
	if err != nil {
		return nil, err
	}
	if claims, ok := t.Claims.(jwt.MapClaims); ok && t.Valid {
		return claims, nil
	}
	return nil, jwt.ErrTokenInvalidClaims
}

func SetCookie(w http.ResponseWriter, name, value string, expires time.Time) {
	secure := config.Cfg.NodeEnv == "production"
	sameSite := http.SameSiteLaxMode
	if secure {
		sameSite = http.SameSiteNoneMode
	}
	http.SetCookie(w, &http.Cookie{
		Name: name, Value: value, Path: "/", HttpOnly: true,
		Secure: secure, SameSite: sameSite, Expires: expires,
	})
}

// --- Redis singleton ---
var (
	redisOnce sync.Once
	rdbClient *redis.Client
)

func redisClient() *redis.Client {
	redisOnce.Do(func() {
		opt, _ := redis.ParseURL(config.Cfg.RedisURL)
		rdbClient = redis.NewClient(opt)
	})
	return rdbClient
}

// --- Response cache (public catalog reads) ---

// CacheGet returns a previously cached JSON response body, if present.
func CacheGet(ctx context.Context, key string) (string, bool) {
	val, err := redisClient().Get(ctx, key).Result()
	if err != nil {
		return "", false
	}
	return val, true
}

// CacheSet stores a JSON response body under key for ttl.
func CacheSet(ctx context.Context, key, value string, ttl time.Duration) {
	redisClient().Set(ctx, key, value, ttl)
}

// CacheDelPattern removes every key matching a glob pattern (e.g. "cache:products:*").
// Used to invalidate cached reads after an admin write, since cached keys are
// per-query-string and there's no single key to target directly.
func CacheDelPattern(ctx context.Context, pattern string) {
	rdb := redisClient()
	var keys []string
	iter := rdb.Scan(ctx, 0, pattern, 0).Iterator()
	for iter.Next(ctx) {
		keys = append(keys, iter.Val())
	}
	if len(keys) > 0 {
		rdb.Del(ctx, keys...)
	}
}
