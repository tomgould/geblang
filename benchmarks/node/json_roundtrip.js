"use strict";

const n = process.argv[2] !== undefined ? parseInt(process.argv[2], 10) : 200;

const payload = {
    name: "Geblang",
    version: "1.2.0",
    tags: ["script", "static", "decimals"],
    metrics: { users: 12345, posts: 678910, active: true },
    items: [
        { id: 1, title: "alpha", score: 95, labels: ["x", "y"] },
        { id: 2, title: "beta",  score: 80, labels: ["x", "z"] },
        { id: 3, title: "gamma", score: 75, labels: ["z"] },
        { id: 4, title: "delta", score: 60, labels: ["y", "z"] },
    ],
};

const records = [];
for (let i = 0; i < 800; i++) {
    records.push(payload);
}
const bulk = { records };

const text = JSON.stringify(bulk);
const textLen = text.length;

let total = 0;
for (let i = 0; i < n; i++) {
    const parsed = JSON.parse(text);
    const again = JSON.stringify(parsed);
    total += again.length;
}

console.log(textLen);
console.log(total);
