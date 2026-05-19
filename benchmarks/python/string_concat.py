import sys

n = int(sys.argv[1]) if len(sys.argv) > 1 else 20_000
acc = ""

for i in range(n):
    if i % 7 == 0:
        acc += "x"
    elif i % 3 == 0:
        acc += "ab"
    else:
        acc += "1"

print(len(acc))
