import sys
from functools import reduce

n = int(sys.argv[1]) if len(sys.argv) > 1 else 10_000

items = list(range(n))
evens = list(filter(lambda x: x % 2 == 0, items))
squared = list(map(lambda x: x * x, evens))
total = reduce(lambda a, b: a + b, squared, 0)

print(total)
