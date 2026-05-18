import sys

n = int(sys.argv[1]) if len(sys.argv) > 1 else 2_000_000
total = 0

for i in range(n):
    if i % 3 == 0:
        total += i
    else:
        total -= 1

print(total)

