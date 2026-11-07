"""Generated events for C8 (story 54.16): 50 videos with random metadata, as the harness's
fixtures file (the mock plugin API's answers)."""
import json
import random
import sys

random.seed(5416)
words = ["قصة", "story", "news", "أخبار", "match", "lecture", "درس", "music", "film", "حلقة"]
videos = []
for i in range(50):
    videos.append({
        "id": f"v{i}",
        "title": " ".join(random.choice(words) for _ in range(random.randint(1, 6))),
        "duration_sec": random.randint(1, 4 * 3600),
        "year": random.choice([None, random.randint(1950, 2026)]),
        "language": random.choice(["ar", "en", ""]),
        "tags": random.sample(words, random.randint(0, 4)),
    })
json.dump({"library/video": videos[0], "library/videos": {"items": videos}, "transcript/segments": {"segments": []}}, open(sys.argv[1], "w"))
