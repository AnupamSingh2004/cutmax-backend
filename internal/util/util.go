package util

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"net/http"
	"strconv"
	"time"
)

// ===== Response helpers =====

func JsonOK(w http.ResponseWriter, status int, data map[string]interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	data["success"] = true
	json.NewEncoder(w).Encode(data)
}

func JsonErr(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "error": msg})
}

func Decode(r *http.Request, v interface{}) error { return json.NewDecoder(r.Body).Decode(v) }

func GenRef() string {
	now := time.Now()
	return fmt.Sprintf("ENQ-%02d%02d%02d-%06d", now.Year()%100, now.Month(), now.Day(), rand.Intn(1000000))
}

func Atoi(s string, def int) int {
	if i, err := strconv.Atoi(s); err == nil && i > 0 {
		return i
	}
	return def
}

func OrDef(s, def string) string {
	if s != "" {
		return s
	}
	return def
}

func NullStr(s string) interface{} {
	if s == "" {
		return nil
	}
	return s
}
