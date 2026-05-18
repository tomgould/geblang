<?php
$n = isset($argv[1]) ? (int) $argv[1] : 5000;
$values = [];

for ($i = 0; $i < $n; $i++) {
    $values[] = $i;
}

$total = 0;
foreach ($values as $value) {
    if ($value % 5 === 0) {
        $total += $value;
    }
}

echo $total, PHP_EOL;
