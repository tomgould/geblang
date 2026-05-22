<?php
$n = isset($argv[1]) ? (int) $argv[1] : 200;

$payload = [
    "name" => "Geblang",
    "version" => "1.2.0",
    "tags" => ["script", "static", "decimals"],
    "metrics" => ["users" => 12345, "posts" => 678910, "active" => true],
    "items" => [
        ["id" => 1, "title" => "alpha", "score" => 95, "labels" => ["x", "y"]],
        ["id" => 2, "title" => "beta",  "score" => 80, "labels" => ["x", "z"]],
        ["id" => 3, "title" => "gamma", "score" => 75, "labels" => ["z"]],
        ["id" => 4, "title" => "delta", "score" => 60, "labels" => ["y", "z"]],
    ],
];

$records = [];
for ($i = 0; $i < 800; $i++) {
    $records[] = $payload;
}
$bulk = ["records" => $records];

$text = json_encode($bulk);
$textLen = strlen($text);

$total = 0;
for ($i = 0; $i < $n; $i++) {
    $parsed = json_decode($text, true);
    $again = json_encode($parsed);
    $total += strlen($again);
}

echo $textLen, PHP_EOL;
echo $total, PHP_EOL;
