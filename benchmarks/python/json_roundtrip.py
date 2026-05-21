import sys
import json

n = int(sys.argv[1]) if len(sys.argv) > 1 else 200

payload = {
    "name": "Geblang",
    "version": "1.0.5",
    "tags": ["script", "static", "decimals"],
    "metrics": {"users": 12345, "posts": 678910, "active": True},
    "items": [
        {"id": 1, "title": "alpha", "score": 95, "labels": ["x", "y"]},
        {"id": 2, "title": "beta",  "score": 80, "labels": ["x", "z"]},
        {"id": 3, "title": "gamma", "score": 75, "labels": ["z"]},
        {"id": 4, "title": "delta", "score": 60, "labels": ["y", "z"]},
    ],
}

bulk = {"records": [payload for _ in range(800)]}

text = json.dumps(bulk, separators=(",", ":"))
text_len = len(text)

total = 0
for _ in range(n):
    parsed = json.loads(text)
    again = json.dumps(parsed, separators=(",", ":"))
    total += len(again)

print(text_len)
print(total)
