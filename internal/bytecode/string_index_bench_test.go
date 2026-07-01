package bytecode_test

import (
	"fmt"
	"testing"
)

// BenchmarkStringIndexScan* prove O(n) index-scan scaling after the rune-index cache (pre-fix: O(n^2) via []rune per call).
func BenchmarkStringIndexScan5k(b *testing.B)  { benchStringIndexScan(b, 5_000) }
func BenchmarkStringIndexScan20k(b *testing.B) { benchStringIndexScan(b, 20_000) }
func BenchmarkStringIndexScan80k(b *testing.B) { benchStringIndexScan(b, 80_000) }
func BenchmarkStringIndexScanUnicode20k(b *testing.B) {
	const src = `
import io;
import string;
string pair = string.fromCodePoints([233, 20013]);
string s = pair.repeat(10000);
int sum = 0;
for (int i = 0; i < s.length(); i++) {
    sum = sum + s[i].length();
}
io.println(sum);
`
	chunk := compileSource(b, src)
	b.ResetTimer()
	for range b.N {
		runChunk(b, chunk)
	}
	b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(int64(b.N)*20_000), "ns/char")
}

func benchStringIndexScan(b *testing.B, n int) {
	src := fmt.Sprintf(`
import io;
string s = "a".repeat(%d);
int sum = 0;
for (int i = 0; i < %d; i++) {
    sum = sum + s[i].length();
}
io.println(sum);
`, n, n)
	chunk := compileSource(b, src)
	b.ResetTimer()
	for range b.N {
		runChunk(b, chunk)
	}
	b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(int64(b.N)*int64(n)), "ns/char")
}

// BenchmarkStringConcatCreation confirms the concat path is unaffected (StringRuneInfo is never called there).
func BenchmarkStringConcatCreation(b *testing.B) {
	const src = `
import io;
string acc = "";
for (int i = 0; i < 20000; i++) {
    if (i % 7 == 0) {
        acc = acc + "x";
    } else if (i % 3 == 0) {
        acc = acc + "ab";
    } else {
        acc = acc + "1";
    }
}
io.println(acc.length());
`
	chunk := compileSource(b, src)
	b.ResetTimer()
	for range b.N {
		runChunk(b, chunk)
	}
}
