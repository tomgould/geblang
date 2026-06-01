"use strict";

const n = process.argv[2] !== undefined ? parseInt(process.argv[2], 10) : 100_000;
const pattern = /[a-z]+[0-9]+/;
const samples = ["foo123", "bar9", "noatch", "ABC", "xy42z", "z0"];

let hits = 0;
for (let i = 0; i < n; i++) {
    const s = samples[i % samples.length];
    if (pattern.test(s)) {
        hits++;
    }
}

console.log(hits);
