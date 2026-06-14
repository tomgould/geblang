package transpilert

import (
	mrand "math/rand"
	"time"
)

// Typed adapters for the random module's scalar surface over Go's math/rand,
// the same generator the interpreter uses, so a seeded sequence is identical
// across backends. The package-default generator starts time-seeded; seed()
// reseeds it for reproducibility. The generator-handle and list (choice /
// shuffle) overloads are not bridged here (they need runtime list/handle
// values).

var randomDefault = mrand.New(mrand.NewSource(time.Now().UnixNano()))

func RandomSeed(seed int64) any {
	randomDefault.Seed(seed)
	return nil
}

func RandomNext() int64 { return randomDefault.Int63() }

func RandomIntRange(min, max int64) int64 {
	return min + randomDefault.Int63n(max-min)
}

func RandomFloat() float64 { return randomDefault.Float64() }

func RandomBool() bool { return randomDefault.Intn(2) == 1 }
