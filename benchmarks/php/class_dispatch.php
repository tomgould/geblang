<?php
class Counter {
    public int $value;

    public function __construct(int $start) {
        $this->value = $start;
    }

    public function step(int $delta): int {
        $this->value = $this->value + $delta;
        return $this->value;
    }

    public function double_(): int {
        $this->value = $this->value * 2;
        return $this->value;
    }
}

$n = isset($argv[1]) ? (int) $argv[1] : 50000;

$c = new Counter(0);
$total = 0;

for ($i = 0; $i < $n; $i++) {
    if ($i % 100 === 0) {
        $c->value = $i;
        $total += $c->double_();
    } else {
        $total += $c->step(1);
    }
}

echo $total, PHP_EOL;
