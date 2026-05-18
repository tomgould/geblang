<?php
$n = isset($argv[1]) ? (int) $argv[1] : 2000000;
$total = 0;

for ($i = 0; $i < $n; $i++) {
    if ($i % 3 === 0) {
        $total += $i;
    } else {
        $total -= 1;
    }
}

echo $total, PHP_EOL;

