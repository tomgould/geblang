//go:build windows

package native

// coder/hnsw does not build on Windows, so the hnsw module uses the exact flat backend.
func newAnnIndex(metric string, m, ef int) (annIndex, error) {
	return newFlatIndex(metric)
}
