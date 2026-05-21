<?php
$n = isset($argv[1]) ? (int) $argv[1] : 10000;

$items = range(0, $n - 1);
$evens = array_filter($items, fn($x) => $x % 2 === 0);
$squared = array_map(fn($x) => $x * $x, $evens);
$total = array_reduce($squared, fn($a, $b) => $a + $b, 0);

echo $total, PHP_EOL;
