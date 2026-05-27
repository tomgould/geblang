package concurrent

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestUnboundedAcquireNeverBlocks(t *testing.T) {
	p := NewPool(0, 0, Reject)
	for i := 0; i < 1000; i++ {
		if err := p.Acquire(context.Background()); err != nil {
			t.Fatalf("unbounded acquire returned error: %v", err)
		}
	}
	if !p.IsUnbounded() {
		t.Fatalf("expected IsUnbounded true")
	}
	if got := p.Stats().Rejected; got != 0 {
		t.Fatalf("rejected: got %d want 0", got)
	}
}

func TestRejectReturnsErrOverloadedWhenFull(t *testing.T) {
	p := NewPool(1, 0, Reject)
	if err := p.Acquire(context.Background()); err != nil {
		t.Fatalf("first acquire failed: %v", err)
	}
	if err := p.Acquire(context.Background()); err != ErrOverloaded {
		t.Fatalf("second acquire: got %v, want ErrOverloaded", err)
	}
	if got := p.Stats().Rejected; got != 1 {
		t.Fatalf("rejected: got %d want 1", got)
	}
	p.Release()
	if err := p.Acquire(context.Background()); err != nil {
		t.Fatalf("post-release acquire failed: %v", err)
	}
}

func TestWaitBlocksUntilSlotFree(t *testing.T) {
	p := NewPool(1, 0, Wait)
	if err := p.Acquire(context.Background()); err != nil {
		t.Fatalf("first acquire failed: %v", err)
	}
	done := make(chan struct{})
	go func() {
		if err := p.Acquire(context.Background()); err != nil {
			t.Errorf("waiting acquire failed: %v", err)
		}
		close(done)
	}()
	select {
	case <-done:
		t.Fatal("second acquire returned before slot was free")
	case <-time.After(50 * time.Millisecond):
	}
	p.Release()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("waiting acquire did not unblock after release")
	}
}

func TestQueueFlushesOverflowWhenFull(t *testing.T) {
	p := NewPool(1, 2, Reject)
	if err := p.Acquire(context.Background()); err != nil {
		t.Fatalf("active acquire failed: %v", err)
	}
	waited := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() { waited <- p.Acquire(context.Background()) }()
	}
	time.Sleep(20 * time.Millisecond)
	if got := p.Stats().Queued; got != 2 {
		t.Fatalf("queued: got %d want 2", got)
	}
	if err := p.Acquire(context.Background()); err != ErrOverloaded {
		t.Fatalf("queue-full acquire: got %v want ErrOverloaded", err)
	}
	p.Release()
	if err := <-waited; err != nil {
		t.Fatalf("queued acquire 1 failed: %v", err)
	}
	p.Release()
	if err := <-waited; err != nil {
		t.Fatalf("queued acquire 2 failed: %v", err)
	}
}

func TestDropReturnsErrOverloaded(t *testing.T) {
	p := NewPool(1, 0, Drop)
	_ = p.Acquire(context.Background())
	if err := p.Acquire(context.Background()); err != ErrOverloaded {
		t.Fatalf("drop policy acquire: got %v want ErrOverloaded", err)
	}
	if p.Policy() != Drop {
		t.Fatalf("policy: got %v want Drop", p.Policy())
	}
}

func TestContextCancelInterruptsWait(t *testing.T) {
	p := NewPool(1, 0, Wait)
	_ = p.Acquire(context.Background())
	ctx, cancel := context.WithCancel(context.Background())
	out := make(chan error, 1)
	go func() { out <- p.Acquire(ctx) }()
	time.Sleep(20 * time.Millisecond)
	cancel()
	select {
	case err := <-out:
		if err != context.Canceled {
			t.Fatalf("waiting acquire after cancel: got %v want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("cancelled acquire did not return")
	}
}

func TestStatsAreConcurrencySafe(t *testing.T) {
	p := NewPool(8, 0, Wait)
	var wg sync.WaitGroup
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = p.Acquire(context.Background())
			time.Sleep(time.Millisecond)
			p.Release()
		}()
	}
	wg.Wait()
	stats := p.Stats()
	if stats.Active != 0 {
		t.Fatalf("active leaked: got %d want 0", stats.Active)
	}
	if stats.MaxConcurrent != 8 {
		t.Fatalf("maxConcurrent: got %d want 8", stats.MaxConcurrent)
	}
}

func TestParsePolicyFallsBackToReject(t *testing.T) {
	cases := map[string]OverloadPolicy{
		"reject":  Reject,
		"wait":    Wait,
		"drop":    Drop,
		"":        Reject,
		"unknown": Reject,
	}
	for input, want := range cases {
		if got := ParsePolicy(input); got != want {
			t.Errorf("ParsePolicy(%q) = %v, want %v", input, got, want)
		}
	}
}
