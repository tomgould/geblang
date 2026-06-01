"use strict";

const n = process.argv[2] !== undefined ? parseInt(process.argv[2], 10) : 20_000;
let acc = "";

for (let i = 0; i < n; i++) {
    if (i % 7 === 0) {
        acc += "x";
    } else if (i % 3 === 0) {
        acc += "ab";
    } else {
        acc += "1";
    }
}

console.log(acc.length);
