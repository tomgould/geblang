package transpilert

import "time"

// Task is an async result computed on a goroutine; mirrors the interpreter's
// async value (Await blocks for the result, Done reports completion).
type Task[T any] struct {
	done chan struct{}
	val  T
}

// awaitable lets heterogeneous tasks be awaited through a single interface
// (Go generics cannot hold a []Task[T] of mixed T).
type awaitable interface{ awaitAny() any }

// Run starts fn on a goroutine and returns a Task for its result.
func Run[T any](fn func() T) *Task[T] {
	t := &Task[T]{done: make(chan struct{})}
	go func() {
		t.val = fn()
		close(t.done)
	}()
	return t
}

// Await blocks until the task completes and returns its result.
func (t *Task[T]) Await() T {
	<-t.done
	return t.val
}

// Done reports whether the task has completed without blocking.
func (t *Task[T]) Done() bool {
	select {
	case <-t.done:
		return true
	default:
		return false
	}
}

func (t *Task[T]) awaitAny() any { return t.Await() }

// Sleep returns a task that completes after ms milliseconds.
func Sleep(ms int64) *Task[any] {
	return Run(func() any {
		if ms > 0 {
			time.Sleep(time.Duration(ms) * time.Millisecond)
		}
		return nil
	})
}

// All awaits every task and returns their results in order.
func All(tasks []awaitable) *Task[[]any] {
	return Run(func() []any {
		out := make([]any, len(tasks))
		for i, t := range tasks {
			out[i] = t.awaitAny()
		}
		return out
	})
}

// Race returns the result of the first task to complete.
func Race(tasks []awaitable) *Task[any] {
	return Run(func() any {
		result := make(chan any, len(tasks))
		for _, t := range tasks {
			t := t
			go func() { result <- t.awaitAny() }()
		}
		return <-result
	})
}

// AsAwaitable adapts a typed task to the heterogeneous awaitable interface
// for use with All / Race.
func AsAwaitable[T any](t *Task[T]) awaitable { return t }
