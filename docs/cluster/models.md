# The transcription model on the share

After Whisper, Maktaba listens to each lecture a second time with **Cohere Transcribe
Arabic** (`CohereLabs/cohere-transcribe-arabic-07-2026`) and keeps the better words of the
two. That model is free to use (Apache 2.0), but Hugging Face only gives it to accounts that
have accepted its licence.

**Only the home needs the Hugging Face licence and login.** The other machines get the model
from the home, through the NAS share, the same way they get everything else. The Hugging Face
login stays on the home, like the Plex token.

## On the home, once

1. Open <https://huggingface.co/CohereLabs/cohere-transcribe-arabic-07-2026> while signed in
   to Hugging Face and accept the licence.
2. Log in on the home (it asks for a token from your Hugging Face settings):

   ```sh
   hf auth login
   ```

The first time the home transcribes a lecture, it downloads the model and then copies it to
the share, in the background:

```
maktaba/models/
  CohereLabs__cohere-transcribe-arabic-07-2026/
    c3e911b4…/                 one folder per version of the model
      manifest.json            every file with its size and checksum
      config.json  model.safetensors  …
```

The copy appears only when it is complete: it is written under a hidden name first and
renamed at the end. A copy cut off half way is never used.

## On the other machines: nothing to do

A machine that cannot get the model from Hugging Face takes the newest complete copy on the
share. It checks every file against the checksums in `manifest.json`, keeps its own copy in
`~/.maktaba/models/`, and loads from that. The copy happens once (about 4 GB). After that,
the machine uses its own copy, even when the share is away.

The second listen runs on Apple Silicon Macs, and on Windows and Linux machines with an
NVIDIA GPU that holds it beside Whisper: 10 GB or more on Windows, 8 GB or more on Linux, a
Turing card (RTX 20 series) or newer, and a driver of R580 or later. Other machines don't need
the model and don't download it (a Windows machine also gets the smaller, CPU-only torch):
their transcripts are Whisper's alone, and say why. While the second listen is on, such
machines leave transcription to the ones that run it, unless Settings, Transcription,
"Transcribe without the second listen" lets them; a machine that runs it gives their
transcripts the second listen later.

## Checking which copy a lecture used

Each transcript's `metadata.refine.source` says where the model came from:

| `source` | Meaning |
|---|---|
| `hf` | Hugging Face (the home, or any machine with its own login) |
| `share` | the home's copy on the share |
| `skipped` | no copy anywhere: the transcript is Whisper's alone, and `skipped` says why |

A machine that skipped looks again on a later lecture (after a failed load, after 10 minutes,
then longer), so it starts using the model once the home has put a copy on the share (or, on a
PC, as soon as its GPU has room for it again). On a PC, `metadata.refine.runtime` also says what heard the lecture, for example
`transformers 5.17.0, torch 2.14.1+cu130, cuda:0 float16`.

## Settings

- `MAKTABA_MODELS_DIR` puts the shared copies in another folder (on every machine).
- `MAKTABA_STT_REFINE=0` turns the second listen off.
