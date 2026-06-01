"use strict";

const n = process.argv[2] !== undefined ? parseInt(process.argv[2], 10) : 10_000;
const d = Object.create(null);

for (let i = 0; i < n; i++) {
    d["k" + i] = i;
}

let total = 0;
for (let i = 0; i < n; i++) {
    const key = "k" + i;
    if (Object.prototype.hasOwnProperty.call(d, key)) {
        total += d[key];
    }
}

console.log(total);
