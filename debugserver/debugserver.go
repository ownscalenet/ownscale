// Package debugserver exposes pprof and runtime memory stats for diagnosing
// memory usage. It must only be bound to a loopback address.
package debugserver

import (
	"encoding/json"
	"net/http"
	"net/http/pprof"
	"os"
	"runtime"
	"runtime/debug"
	"strconv"
	"strings"
)

// Handler returns the debug HTTP handler.
func Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/debug/pprof/", pprof.Index)
	mux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
	mux.HandleFunc("/debug/pprof/profile", pprof.Profile)
	mux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
	mux.HandleFunc("/debug/pprof/trace", pprof.Trace)
	mux.HandleFunc("GET /debug/mem", memHandler)
	mux.HandleFunc("POST /debug/gc", func(w http.ResponseWriter, r *http.Request) {
		debug.FreeOSMemory()
		memHandler(w, r)
	})
	return mux
}

// MemStats is a snapshot of process and Go runtime memory, in bytes.
type MemStats struct {
	RSS          int64  `json:"rss"`
	RSSAnon      int64  `json:"rss_anon"`
	RSSFile      int64  `json:"rss_file"`
	Sys          uint64 `json:"sys"`
	HeapAlloc    uint64 `json:"heap_alloc"`
	HeapInuse    uint64 `json:"heap_inuse"`
	HeapIdle     uint64 `json:"heap_idle"`
	HeapReleased uint64 `json:"heap_released"`
	StackInuse   uint64 `json:"stack_inuse"`
	OtherSys     uint64 `json:"other_sys"`
	NextGC       uint64 `json:"next_gc"`
	NumGC        uint32 `json:"num_gc"`
	Goroutines   int    `json:"goroutines"`
}

// ReadMemStats collects a MemStats snapshot.
func ReadMemStats() MemStats {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	s := MemStats{
		Sys:          m.Sys,
		HeapAlloc:    m.HeapAlloc,
		HeapInuse:    m.HeapInuse,
		HeapIdle:     m.HeapIdle,
		HeapReleased: m.HeapReleased,
		StackInuse:   m.StackInuse,
		OtherSys:     m.OtherSys,
		NextGC:       m.NextGC,
		NumGC:        m.NumGC,
		Goroutines:   runtime.NumGoroutine(),
	}
	s.RSS, s.RSSAnon, s.RSSFile = procRSS()
	return s
}

func memHandler(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(ReadMemStats())
}

// procRSS reads RSS from /proc; zeros on non-Linux.
func procRSS() (total, anon, file int64) {
	b, err := os.ReadFile("/proc/self/status")
	if err != nil {
		return 0, 0, 0
	}
	for _, line := range strings.Split(string(b), "\n") {
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		f := strings.Fields(v)
		if len(f) == 0 {
			continue
		}
		n, _ := strconv.ParseInt(f[0], 10, 64)
		switch k {
		case "VmRSS":
			total = n * 1024
		case "RssAnon":
			anon = n * 1024
		case "RssFile":
			file = n * 1024
		}
	}
	return total, anon, file
}
