import sys
import re

n = int(sys.argv[1]) if len(sys.argv) > 1 else 100_000
pattern = re.compile("[a-z]+[0-9]+")
samples = ["foo123", "bar9", "noatch", "ABC", "xy42z", "z0"]

hits = 0
for i in range(n):
    s = samples[i % len(samples)]
    if pattern.search(s):
        hits += 1

print(hits)
