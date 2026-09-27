package utils

import (
	"maps"
	"testing"
)

func TestMapRange(t *testing.T) {
	tests := []struct {
		name string
		// visit handles one entry during Range and reports whether to go on.
		visit     func(m *Map[string, int], key string) bool
		wantSeen  int
		wantAfter map[string]int
	}{
		{
			name:      "visits every entry",
			visit:     func(*Map[string, int], string) bool { return true },
			wantSeen:  3,
			wantAfter: map[string]int{"a": 1, "b": 2, "c": 3},
		},
		{
			name:      "stops when f returns false",
			visit:     func(*Map[string, int], string) bool { return false },
			wantSeen:  1,
			wantAfter: map[string]int{"a": 1, "b": 2, "c": 3},
		},
		{
			name: "allows deleting while ranging",
			visit: func(m *Map[string, int], key string) bool {
				m.Delete(key)
				return true
			},
			wantSeen:  3,
			wantAfter: map[string]int{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var m Map[string, int]
			for k, v := range map[string]int{"a": 1, "b": 2, "c": 3} {
				m.Store(k, v)
			}

			seen := 0
			for k, v := range m.Range {
				seen++
				if want := map[string]int{"a": 1, "b": 2, "c": 3}[k]; v != want {
					t.Errorf("Range gave %q = %d, want %d", k, v, want)
				}
				if !tt.visit(&m, k) {
					break
				}
			}
			if seen != tt.wantSeen {
				t.Errorf("visited %d entries, want %d", seen, tt.wantSeen)
			}

			after := map[string]int{}
			m.Range(func(k string, v int) bool {
				after[k] = v
				return true
			})
			if !maps.Equal(after, tt.wantAfter) {
				t.Errorf("map after Range = %v, want %v", after, tt.wantAfter)
			}
		})
	}
}
