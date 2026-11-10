# The speech measure's clips (Epic 59 story 59.06)

`stt/benchmark.py` transcribes these two 30-second clips to measure how fast this machine
turns speech into text (the real-time factor) and, in CI, how well (the word error rate
against the words spoken).

| File | What it is |
|---|---|
| `ar.ogg`, `ar.txt` | Arabic: a man reading *Kalila wa Dimna* (Ibn al-Muqaffa'), the start of the story of the hermit and the weasel: 29.3 s of speech, then silence to 30 s |
| `en.ogg`, `en.txt` | English: a text written for Maktaba, spoken by eSpeak NG: 29.2 s of speech, then silence to 30 s |

Both are Opus at 32 kb/s, mono, 16 kHz, with no metadata.

**Licence: public domain.**

- `ar.ogg` is cut from the LibriVox recording of كليلة ودمنة, chapter 16 (الباب الخامس:
  الناسك وابن عرس), <https://archive.org/details/kalilawadimna_2608_librivox>, released by
  LibriVox under the Public Domain Mark 1.0; the text is Ibn al-Muqaffa''s, from the 8th
  century. `ar.txt` is what the reader says in the clip (his edition words the story
  differently from Wikisource's), checked against four Whisper models' transcripts.
- `en.txt` was written for Maktaba and is dedicated to the public domain (CC0 1.0). `en.ogg`
  was spoken from it by eSpeak NG 1.51 (voice `en-gb`, 135 words a minute); eSpeak NG's
  licence (GPL-3.0) covers the program, not the speech it makes.

How they were made (ffmpeg in a `debian:12-slim` container):

```sh
ffmpeg -ss 54.6 -t 29.3 -i kalilawadimna_16_ibnalmuqaffa_64kb.mp3 \
    -af 'afade=t=in:d=0.05,afade=t=out:st=29.1:d=0.2,apad=whole_dur=30' -t 30 \
    -ac 1 -ar 16000 -c:a libopus -b:a 32k -map_metadata -1 -fflags +bitexact -flags:a +bitexact ar.ogg
espeak-ng -v en-gb -s 135 -w en.wav -f en.txt
ffmpeg -i en.wav -af apad=whole_dur=30 -t 30 \
    -ac 1 -ar 16000 -c:a libopus -b:a 32k -map_metadata -1 -fflags +bitexact -flags:a +bitexact en.ogg
```

The word error rates are a regression check per model and architecture (a baseline, then no
rise over 5 points: `tests/nas/stt-baseline.json`), not a measure of accuracy on a
household's own recordings. eSpeak NG's Arabic is not intelligible to Whisper, hence a
recording for Arabic.
