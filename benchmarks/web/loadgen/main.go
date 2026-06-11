// loadgen is a minimal closed-loop HTTP load generator for the
// serve-path benchmarks: N workers issue keep-alive GETs for the
// duration and report rps plus latency percentiles.
package main

import (
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

func main() {
	url := flag.String("url", "http://localhost:8201/json/7", "target URL")
	concurrency := flag.Int("c", 16, "concurrent workers")
	duration := flag.Duration("d", 15*time.Second, "test duration")
	flag.Parse()

	transport := &http.Transport{
		MaxIdleConns:        *concurrency * 2,
		MaxIdleConnsPerHost: *concurrency * 2,
	}
	client := &http.Client{Transport: transport, Timeout: 30 * time.Second}

	var ok, fail atomic.Int64
	latencies := make([][]time.Duration, *concurrency)
	deadline := time.Now().Add(*duration)
	var wg sync.WaitGroup
	for w := 0; w < *concurrency; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			lats := make([]time.Duration, 0, 1<<16)
			for time.Now().Before(deadline) {
				start := time.Now()
				resp, err := client.Get(*url)
				if err != nil {
					fail.Add(1)
					continue
				}
				_, _ = io.Copy(io.Discard, resp.Body)
				resp.Body.Close()
				if resp.StatusCode == 200 {
					ok.Add(1)
					lats = append(lats, time.Since(start))
				} else {
					fail.Add(1)
				}
			}
			latencies[w] = lats
		}(w)
	}
	wg.Wait()

	var all []time.Duration
	for _, lats := range latencies {
		all = append(all, lats...)
	}
	if len(all) == 0 {
		fmt.Fprintln(os.Stderr, "no successful requests")
		os.Exit(1)
	}
	sort.Slice(all, func(i, j int) bool { return all[i] < all[j] })
	pct := func(p float64) time.Duration { return all[int(float64(len(all)-1)*p)] }
	rps := float64(ok.Load()) / duration.Seconds()
	fmt.Printf("c=%d ok=%d fail=%d rps=%.0f p50=%v p95=%v p99=%v\n",
		*concurrency, ok.Load(), fail.Load(), rps, pct(0.50), pct(0.95), pct(0.99))
}
