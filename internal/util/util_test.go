package util

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
)

func TestGenRef(t *testing.T) {
	ref := GenRef()
	if ref == "" {
		t.Fatal("GenRef returned empty string")
	}
	// Should start with ENQ-
	if len(ref) < 4 || ref[:4] != "ENQ-" {
		t.Errorf("GenRef prefix = %q, want ENQ-", ref[:min(4, len(ref))])
	}
	// Two calls should produce different refs
	ref2 := GenRef()
	if ref == ref2 {
		t.Error("GenRef produced duplicate references")
	}
}

func TestNullStr(t *testing.T) {
	if NullStr("") != nil {
		t.Error("NullStr('') should return nil")
	}
	if NullStr("hello") != "hello" {
		t.Error("NullStr('hello') should return 'hello'")
	}
}

func TestCSRFTokenUniqueness(t *testing.T) {
	// Test that GenRef produces unique values (used as reference pattern)
	seen := make(map[string]bool)
	for i := 0; i < 100; i++ {
		ref := GenRef()
		if seen[ref] {
			t.Errorf("duplicate reference on iteration %d: %s", i, ref)
		}
		seen[ref] = true
	}
}

func TestJSONResponseHelpers(t *testing.T) {
	t.Run("JsonOK", func(t *testing.T) {
		w := httptest.NewRecorder()
		JsonOK(w, 200, map[string]interface{}{"foo": "bar"})
		if w.Code != 200 {
			t.Errorf("JsonOK status = %d, want 200", w.Code)
		}
		var resp map[string]interface{}
		json.NewDecoder(w.Body).Decode(&resp)
		if resp["foo"] != "bar" {
			t.Errorf("JsonOK foo = %v, want bar", resp["foo"])
		}
		if resp["success"] != true {
			t.Error("JsonOK should set success=true")
		}
	})

	t.Run("JsonErr", func(t *testing.T) {
		w := httptest.NewRecorder()
		JsonErr(w, 400, "bad request")
		if w.Code != 400 {
			t.Errorf("JsonErr status = %d, want 400", w.Code)
		}
		var resp map[string]interface{}
		json.NewDecoder(w.Body).Decode(&resp)
		if resp["error"] != "bad request" {
			t.Errorf("JsonErr error = %v, want 'bad request'", resp["error"])
		}
		if resp["success"] != false {
			t.Error("JsonErr should set success=false")
		}
	})
}
