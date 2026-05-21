<?php
$n = isset($argv[1]) ? (int) $argv[1] : 100000;
$pattern = "/[a-z]+[0-9]+/";
$samples = ["foo123", "bar9", "noatch", "ABC", "xy42z", "z0"];
$count = count($samples);

$hits = 0;
for ($i = 0; $i < $n; $i++) {
    $s = $samples[$i % $count];
    if (preg_match($pattern, $s)) {
        $hits++;
    }
}

echo $hits, PHP_EOL;
