<?php
$n = isset($argv[1]) ? (int) $argv[1] : 20000;
$acc = "";

for ($i = 0; $i < $n; $i++) {
    if ($i % 7 === 0) {
        $acc .= "x";
    } elseif ($i % 3 === 0) {
        $acc .= "ab";
    } else {
        $acc .= "1";
    }
}

echo strlen($acc), PHP_EOL;
