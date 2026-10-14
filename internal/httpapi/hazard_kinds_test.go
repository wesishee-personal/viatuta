package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestListKindsIsPublicAndComplete checks that clients can read the kind
// table without signing in, and that it carries what they need to render it.
func TestListKindsIsPublicAndComplete(t *testing.T) {
	h := authServer(t).Handler()

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/hazards/kinds", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", rec.Code, rec.Body)
	}

	var body struct {
		Kinds []KindInfo `json:"kinds"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Kinds) < 10 {
		t.Fatalf("only %d kinds returned", len(body.Kinds))
	}

	byKind := map[string]KindInfo{}
	for _, k := range body.Kinds {
		byKind[k.Kind] = k
		if k.Label == "" || k.TTLSeconds <= 0 || k.MaxLifeSeconds <= k.TTLSeconds {
			t.Errorf("%s: incomplete entry %+v", k.Kind, k)
		}
	}
	if got := byKind["accident"].TTLSeconds; got != 3*3600 {
		t.Errorf("accident ttl_s = %d, want %d", got, 3*3600)
	}
	if got := byKind["pothole"].TTLSeconds; got != 7*24*3600 {
		t.Errorf("pothole ttl_s = %d, want %d", got, 7*24*3600)
	}
}
