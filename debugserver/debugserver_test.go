package debugserver

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHandler(t *testing.T) {
	srv := httptest.NewServer(Handler())
	defer srv.Close()

	tests := []struct {
		method, path string
		wantStatus   int
		wantMem      bool
	}{
		{http.MethodGet, "/debug/mem", http.StatusOK, true},
		{http.MethodPost, "/debug/gc", http.StatusOK, true},
		{http.MethodGet, "/debug/gc", http.StatusMethodNotAllowed, false},
		{http.MethodGet, "/debug/pprof/goroutine?debug=1", http.StatusOK, false},
	}
	for _, tt := range tests {
		t.Run(tt.method+" "+tt.path, func(t *testing.T) {
			req, _ := http.NewRequest(tt.method, srv.URL+tt.path, nil)
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != tt.wantStatus {
				t.Fatalf("status = %d, want %d", resp.StatusCode, tt.wantStatus)
			}
			if !tt.wantMem {
				return
			}
			var m MemStats
			if err := json.NewDecoder(resp.Body).Decode(&m); err != nil {
				t.Fatal(err)
			}
			if m.HeapInuse == 0 || m.Goroutines == 0 {
				t.Errorf("MemStats = %+v, want non-zero heap and goroutines", m)
			}
		})
	}
}
