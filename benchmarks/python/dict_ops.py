import sys

n = int(sys.argv[1]) if len(sys.argv) > 1 else 10_000
d = {}

for i in range(n):
    key = "k" + str(i)
    d[key] = i

total = 0
for i in range(n):
    key = "k" + str(i)
    if key in d:
        total += d[key]

print(total)
