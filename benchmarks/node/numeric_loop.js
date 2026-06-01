"use strict";

const n = process.argv[2] !== undefined ? parseInt(process.argv[2], 10) : 2_000_000;
let total = 0;

for (let i = 0; i < n; i++) {
    if (i % 3 === 0) {
        total += i;
    } else {
        total -= 1;
    }
}

console.log(total);
