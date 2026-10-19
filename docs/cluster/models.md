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

The second listen runs only on Apple Silicon Macs. Other machines don't need the model.

## Checking which copy a lecture used

Each transcript's `metadata.refine.source` says where the model came from:

| `source` | Meaning |
|---|---|
| `hf` | Hugging Face (the home, or any machine with its own login) |
| `share` | the home's copy on the share |
| `skipped` | no copy anywhere: the transcript is Whisper's alone, and `skipped` says why |

A machine that skipped looks again on its next lecture, so it starts using the model as soon
as the home has put a copy on the share.

## Settings

- `MAKTABA_MODELS_DIR` puts the shared copies in another folder (on every machine).
- `MAKTABA_STT_REFINE=0` turns the second listen off.
