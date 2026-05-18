<?php
function fib(int $n): int {
    if ($n < 2) {
        return $n;
    }
    return fib($n - 1) + fib($n - 2);
}

$n = isset($argv[1]) ? (int) $argv[1] : 28;
echo fib($n), PHP_EOL;

