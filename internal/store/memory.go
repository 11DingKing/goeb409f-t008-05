package store

import (
	"context"
	"errors"
	"sort"
	"sync"

	"microgrid/internal/domain"
)

// Memory is a concurrency-safe in-memory repository. It deep-clones on read
// and write so callers cannot mutate stored aggregates directly.
type Memory struct {
	mu   sync.RWMutex
	data map[string]*domain.BlackStartProcess
}

// NewMemory creates an empty in-memory repository.
func NewMemory() *Memory {
	return &Memory{data: make(map[string]*domain.BlackStartProcess)}
}

// Save stores a clone of the process.
func (m *Memory) Save(_ context.Context, p *domain.BlackStartProcess) error {
	if p == nil {
		return errors.New("cannot save nil process")
	}
	if p.ID == "" {
		return errors.New("cannot save process with empty id")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.data[p.ID] = p.Clone()
	return nil
}

// Get returns a clone of the process with the given id.
func (m *Memory) Get(_ context.Context, id string) (*domain.BlackStartProcess, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	p, ok := m.data[id]
	if !ok {
		return nil, domain.ErrNotFound
	}
	return p.Clone(), nil
}

// List returns all processes cloned, ordered by creation time.
func (m *Memory) List(_ context.Context) ([]*domain.BlackStartProcess, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]*domain.BlackStartProcess, 0, len(m.data))
	for _, p := range m.data {
		out = append(out, p.Clone())
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out, nil
}

// Active returns clones of all active processes.
func (m *Memory) Active(_ context.Context) ([]*domain.BlackStartProcess, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var out []*domain.BlackStartProcess
	for _, p := range m.data {
		if p.IsActive() {
			out = append(out, p.Clone())
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out, nil
}
