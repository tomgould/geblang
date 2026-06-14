package transpilert

import "sort"

// Typed adapters for Geblang's list higher-order methods on the transpiler's
// unboxed []T. Transpiled Geblang closures are Go funcs, so these take Go
// closures directly. Semantics match the interpreter's primitive dispatch.

// Map applies fn to each element, returning a new slice.
func Map[T, R any](xs []T, fn func(T) R) []R {
	out := make([]R, len(xs))
	for i, x := range xs {
		out[i] = fn(x)
	}
	return out
}

// Filter keeps elements for which fn reports true.
func Filter[T any](xs []T, fn func(T) bool) []T {
	out := []T{}
	for _, x := range xs {
		if fn(x) {
			out = append(out, x)
		}
	}
	return out
}

// Reduce folds xs left-to-right starting from init.
func Reduce[T, A any](xs []T, fn func(A, T) A, init A) A {
	acc := init
	for _, x := range xs {
		acc = fn(acc, x)
	}
	return acc
}

// Find returns the first element matching fn, or nil (Geblang null) on a miss.
func Find[T any](xs []T, fn func(T) bool) any {
	for _, x := range xs {
		if fn(x) {
			return x
		}
	}
	return nil
}

// FindLast returns the last element matching fn, or nil on a miss.
func FindLast[T any](xs []T, fn func(T) bool) any {
	for i := len(xs) - 1; i >= 0; i-- {
		if fn(xs[i]) {
			return xs[i]
		}
	}
	return nil
}

// AnyMatch reports whether any element matches fn.
func AnyMatch[T any](xs []T, fn func(T) bool) bool {
	for _, x := range xs {
		if fn(x) {
			return true
		}
	}
	return false
}

// AllMatch reports whether every element matches fn.
func AllMatch[T any](xs []T, fn func(T) bool) bool {
	for _, x := range xs {
		if !fn(x) {
			return false
		}
	}
	return true
}

// Count returns how many elements match fn.
func Count[T any](xs []T, fn func(T) bool) int64 {
	var n int64
	for _, x := range xs {
		if fn(x) {
			n++
		}
	}
	return n
}

// FlatMap maps each element to a slice and concatenates the results.
func FlatMap[T, R any](xs []T, fn func(T) []R) []R {
	out := []R{}
	for _, x := range xs {
		out = append(out, fn(x)...)
	}
	return out
}

// SortInPlaceCmp stably sorts *xs using a comparator. The comparator returns
// either a bool (less-than) or an int64 (three-way, less when < 0), matching
// the interpreter's sort callback contract; cmpLess adapts the result.
func SortInPlaceCmp[T any](xs *[]T, less func(a, b T) bool) []T {
	sort.SliceStable(*xs, func(i, j int) bool { return less((*xs)[i], (*xs)[j]) })
	return *xs
}

// SortInPlaceBy stably sorts *xs by a selector key (ascending unless desc).
// Keys are compared via the natural ordering the interpreter uses.
func SortInPlaceBy[T any, K any](xs *[]T, key func(T) K, less func(a, b K) bool, desc bool) []T {
	keys := make([]K, len(*xs))
	for i, x := range *xs {
		keys[i] = key(x)
	}
	idx := make([]int, len(*xs))
	for i := range idx {
		idx[i] = i
	}
	sort.SliceStable(idx, func(a, b int) bool {
		if desc {
			return less(keys[idx[b]], keys[idx[a]])
		}
		return less(keys[idx[a]], keys[idx[b]])
	})
	out := make([]T, len(*xs))
	for i, j := range idx {
		out[i] = (*xs)[j]
	}
	copy(*xs, out)
	return *xs
}
