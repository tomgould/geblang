import sys

n = int(sys.argv[1]) if len(sys.argv) > 1 else 5_000
values = []

for i in range(n):
    values.append(i)

total = 0
for value in values:
    if value % 5 == 0:
        total += value

print(total)
