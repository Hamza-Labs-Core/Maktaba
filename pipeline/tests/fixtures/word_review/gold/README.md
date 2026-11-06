# Word review's CI gold subset (Epic 52, story 52.2)

**Synthetic.** These clips were written by hand to exercise every signal (Whisper's
confidence, the second listen's replacements, the dictionary, the Quran check) and the
metrics: no audio, no live data. They stand in for the real gold set until a person has
corrected the reference transcripts the story asks for (3 h of Arabic lectures, 15 min of
Quran inside lectures, 1 h of English, 30 min code-switched, and the 20 disputed spots of
the 2026-10-19 hearing test), which stay on the home with their audio.

- `<clip>.hyp.json`: the stored transcript's sentences, each word as
  `[start, end, text, prob, flags]` (the decoded `word_data`), its `alt`, and the spots.
- `<clip>.ref.txt`: the reference, one sentence per line.
- `baseline.json`: the report the gate (`tools/eval-gate`) compares each PR with.

`python -m maktaba_pipeline.word_review.eval --set ci --out report.json` runs them in
seconds with no model.
