package transpilert

import (
	"sort"
	"testing"
	"time"
)

func TestTaskAwait(t *testing.T) {
	tk := Run(func() int { return 42 })
	if got := tk.Await(); got != 42 {
		t.Fatalf("await = %d, want 42", got)
	}
}

func TestTaskDone(t *testing.T) {
	release := make(chan struct{})
	tk := Run(func() int {
		<-release
		return 1
	})
	if tk.Done() {
		t.Fatal("task reported done before completion")
	}
	close(release)
	tk.Await()
	if !tk.Done() {
		t.Fatal("task not done after await")
	}
}

func TestAllPreservesOrder(t *testing.T) {
	t1 := Run(func() any {
		time.Sleep(20 * time.Millisecond)
		return "a"
	})
	t2 := Run(func() any { return "b" })
	res := All([]awaitable{AsAwaitable(t1), AsAwaitable(t2)}).Await()
	if len(res) != 2 || res[0] != "a" || res[1] != "b" {
		t.Fatalf("all = %v, want [a b]", res)
	}
}

func TestRaceReturnsFirst(t *testing.T) {
	fast := Run(func() any { return "fast" })
	slow := Run(func() any {
		time.Sleep(50 * time.Millisecond)
		return "slow"
	})
	if got := Race([]awaitable{AsAwaitable(slow), AsAwaitable(fast)}).Await(); got != "fast" {
		t.Fatalf("race = %v, want fast", got)
	}
}

func TestSleepCompletes(t *testing.T) {
	start := time.Now()
	Sleep(15).Await()
	if elapsed := time.Since(start); elapsed < 10*time.Millisecond {
		t.Fatalf("sleep returned too early: %v", elapsed)
	}
}

func TestAllConcurrent(t *testing.T) {
	tasks := make([]awaitable, 5)
	for i := 0; i < 5; i++ {
		i := i
		tasks[i] = AsAwaitable(Run(func() any { return i }))
	}
	res := All(tasks).Await()
	got := make([]int, len(res))
	for i, v := range res {
		got[i] = v.(int)
	}
	sort.Ints(got)
	for i := 0; i < 5; i++ {
		if got[i] != i {
			t.Fatalf("all results = %v, want 0..4", got)
		}
	}
}
