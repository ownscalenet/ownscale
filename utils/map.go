package utils

import "sync"

// Map is a generic sync map.
type Map[K comparable, V any] struct {
	m sync.Map
}

func (m *Map[K, V]) Store(key K, value V) {
	m.m.Store(key, value)
}

func (m *Map[K, V]) Load(key K) (V, bool) {
	value, ok := m.m.Load(key)
	if !ok {
		var zero V
		return zero, false
	}

	return value.(V), true
}

func (m *Map[K, V]) Delete(key K) {
	m.m.Delete(key)
}

func (m *Map[K, V]) LoadOrStore(key K, value V) (actual V, loaded bool) {
	actualRaw, loaded := m.m.LoadOrStore(key, value)
	return actualRaw.(V), loaded
}
