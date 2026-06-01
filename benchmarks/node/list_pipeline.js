"use strict";

const n = process.argv[2] !== undefined ? parseInt(process.argv[2], 10) : 5_000;
const values = [];

for (let i = 0; i < n; i++) {
    values.push(i);
}

let total = 0;
for (const value of values) {
    if (value % 5 === 0) {
        total += value;
    }
}

console.log(total);
