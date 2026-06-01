"use strict";

function fib(n) {
    if (n < 2) return n;
    return fib(n - 1) + fib(n - 2);
}

const n = process.argv[2] !== undefined ? parseInt(process.argv[2], 10) : 28;
console.log(fib(n));
