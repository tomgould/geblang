"use strict";

const n = process.argv[2] !== undefined ? parseInt(process.argv[2], 10) : 10_000;

const items = [];
for (let i = 0; i < n; i++) items.push(i);

const evens = items.filter((x) => x % 2 === 0);
const squared = evens.map((x) => x * x);
const total = squared.reduce((a, b) => a + b, 0);

console.log(total);
