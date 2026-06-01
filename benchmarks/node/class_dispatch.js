"use strict";

class Counter {
    constructor(start) {
        this.value = start;
    }

    step(delta) {
        this.value = this.value + delta;
        return this.value;
    }

    double() {
        this.value = this.value * 2;
        return this.value;
    }
}

const n = process.argv[2] !== undefined ? parseInt(process.argv[2], 10) : 50_000;

const c = new Counter(0);
let total = 0;

for (let i = 0; i < n; i++) {
    if (i % 100 === 0) {
        c.value = i;
        total += c.double();
    } else {
        total += c.step(1);
    }
}

console.log(total);
