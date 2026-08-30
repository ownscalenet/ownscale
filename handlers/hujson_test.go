package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestFormatHuJSONPrettyPrints(t *testing.T) {
	h, _ := newTestHandlers(t, false)

	body := `{"content":"{\"acls\":[{\"action\":\"accept\",\"src\":[\"*\"],\"dst\":[\"*:*\"]}]}"}`
	req := httptest.NewRequest(http.MethodPost, "/api/hujson/format", strings.NewReader(body))
	rec := httptest.NewRecorder()
	h.FormatHuJSON(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var resp struct {
		Content string `json:"content"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Content == "" || !strings.Contains(resp.Content, "\n") {
		t.Errorf("content = %q, want multi-line pretty-printed HuJSON", resp.Content)
	}
}

func TestFormatHuJSONRejectsInvalidHuJSON(t *testing.T) {
	h, _ := newTestHandlers(t, false)

	body := `{"content":"not valid hujson {{{"}`
	req := httptest.NewRequest(http.MethodPost, "/api/hujson/format", strings.NewReader(body))
	rec := httptest.NewRecorder()
	h.FormatHuJSON(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d, body=%s", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
}
