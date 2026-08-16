package store

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"microgrid/internal/domain"
)

func TestMemory_SaveGetListActive(t *testing.T) {
	m := NewMemory()
	ctx := context.Background()
	now := time.Now()

	p := domain.NewProcess("a", domain.KindReal, now, now.Add(-time.Minute), domain.DefaultSettings())
	if err := m.Save(ctx, p); err != nil {
		t.Fatal(err)
	}

	got, err := m.Get(ctx, "a")
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != "a" {
		t.Fatalf("id mismatch: %s", got.ID)
	}
	if !got.IsActive() {
		t.Fatal("fresh process should be active")
	}

	if _, err := m.Get(ctx, "missing"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}

	list, err := m.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 {
		t.Fatalf("expected 1 process, got %d", len(list))
	}

	drill := domain.NewSuspendedDrill("d", now, domain.DefaultSettings())
	if err := m.Save(ctx, drill); err != nil {
		t.Fatal(err)
	}
	active, err := m.Active(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(active) != 1 {
		t.Fatalf("expected 1 active (drill suspended), got %d", len(active))
	}
}

func TestMemory_CloneIsolation(t *testing.T) {
	m := NewMemory()
	ctx := context.Background()
	now := time.Now()
	p := domain.NewProcess("a", domain.KindReal, now, now.Add(-time.Minute), domain.DefaultSettings())
	if err := m.Save(ctx, p); err != nil {
		t.Fatal(err)
	}

	got, _ := m.Get(ctx, "a")
	if err := got.MarkStorageOnline(now); err != nil {
		t.Fatal(err)
	}

	again, _ := m.Get(ctx, "a")
	if again.State != domain.StateBlackStartCommanded {
		t.Fatalf("stored state was mutated through clone: %s", again.State)
	}
}

func TestMemory_ConcurrentSave(t *testing.T) {
	m := NewMemory()
	ctx := context.Background()
	var wg sync.WaitGroup
	const n = 50
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			id := fmt.Sprintf("p-%d", i)
			p := domain.NewProcess(id, domain.KindReal, time.Now(), time.Now().Add(-time.Minute), domain.DefaultSettings())
			if err := m.Save(ctx, p); err != nil {
				t.Errorf("save %d: %v", i, err)
			}
		}(i)
	}
	wg.Wait()
	list, _ := m.List(ctx)
	if len(list) != n {
		t.Fatalf("expected %d processes, got %d", n, len(list))
	}
}

func TestMemory_RejectsInvalidInput(t *testing.T) {
	m := NewMemory()
	ctx := context.Background()
	if err := m.Save(ctx, nil); err == nil {
		t.Fatal("expected error saving nil process")
	}
	if err := m.Save(ctx, &domain.BlackStartProcess{}); err == nil {
		t.Fatal("expected error saving process with empty id")
	}
}
