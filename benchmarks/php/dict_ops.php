<?php
$n = isset($argv[1]) ? (int) $argv[1] : 10000;
$d = [];

for ($i = 0; $i < $n; $i++) {
    $key = "k" . $i;
    $d[$key] = $i;
}

$total = 0;
for ($i = 0; $i < $n; $i++) {
    $key = "k" . $i;
    if (array_key_exists($key, $d)) {
        $total += $d[$key];
    }
}

echo $total, PHP_EOL;
