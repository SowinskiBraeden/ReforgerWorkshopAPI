package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gorilla/mux"
)

func TestModByIDRejectsMalformedInput(t *testing.T) {
	app := App{}
	r := httptest.NewRequest(http.MethodGet, "/v1/mod/../../secret", nil)
	r = mux.SetURLVars(r, map[string]string{"id": "../../secret"})
	w := httptest.NewRecorder()

	app.ModByIDHandler(w, r)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
	var body map[string]map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("failed to decode error envelope: %v", err)
	}
	if body["error"]["code"] != "INVALID_MOD_ID" {
		t.Fatalf("error code = %q, want INVALID_MOD_ID", body["error"]["code"])
	}
}
