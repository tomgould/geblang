import sys


class Counter:
    def __init__(self, start):
        self.value = start

    def step(self, delta):
        self.value = self.value + delta
        return self.value

    def double(self):
        self.value = self.value * 2
        return self.value


n = int(sys.argv[1]) if len(sys.argv) > 1 else 50_000

c = Counter(0)
total = 0

for i in range(n):
    if i % 100 == 0:
        c.value = i
        total += c.double()
    else:
        total += c.step(1)

print(total)
