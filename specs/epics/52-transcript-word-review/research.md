# Epic 52 research: word confidence, dictionaries, names, review tools, LLM judges, learning

> For [README.md](README.md). Researched 2026-11-03; every source below was fetched that day.
> Items that could not be confirmed are marked so.
>
> **Decisions, 2026-11-03 ([README §8](README.md#8-decisions-2026-11-03)):** there is no
> LLM judge (story 52.7 removed), so the LLM rows and §5 below are kept as background only; every
> check is deterministic (CAMeL Tools for Arabic, Hunspell for English and ~100 other languages,
> a fallback pack for the rest). Quran verse runs are checked against the bundled Tanzil text
> (story 52.15) instead of only being protected.

## What the research decided

| Finding | Decision in the epic |
|---|---|
| Whisper's own `word_anomaly_score` treats `probability < 0.15` as suspect, and words shorter than 0.133 s or longer than 2 s as odd | The uncalibrated starting threshold for `low_confidence` and a duration check (52.6), replaced by the per-language calibration curve once 52.2 has data |
| Whisper is over-confident in noise (10-20% of wrong tokens above 0.7); temperature scaling cut calibration error by 58% | Calibrate per language and model (52.2). Raw probability is used only to rank |
| A CHI 2025 study (36 people): confidence highlighting alone "neither improved correction efficiency nor was perceived as helpful" | No confidence colour wash. One word at a time, from several signals, each flag showing its reasons (52.11) |
| A word list over-flags Arabic (clitics, inflection) | CAMeL Tools' analyser decides "known"; Hunspell (Ayaspell) only suggests (52.4) |
| CAMeL Tools code is MIT, but its morphology databases are **GPL v2**; Ayaspell is GPL/LGPL/MPL; Farasa is **research-only**; Qalsadi is GPL | Packs fetch dictionary data at install time and never bundle it in the app (as `ensure_camel_data` does today). Farasa is not used |
| An empty CAMeL analysis list is not documented as "unknown" | 52.4 tests it on a known/unknown word list before relying on it |
| NFKC folds presentation forms but does not remove harakat | Mark stripping is the pack's `key()`, not Unicode normalisation (52.3, 52.4) |
| UAX #29 is a tailorable default; Chinese, Japanese, Thai, Lao, Khmer and Burmese need dictionary breaking (ICU has it) | The fallback pack uses ICU word breaks, so those languages work without their own pack (52.3) |
| UAX #24 `Script` / `Script_Extensions`, with Common/Inherited resolved from context, detects code-switching | `word_check` splits sentences into script runs and routes each to its pack (52.3) |
| wordfreq covers Arabic and ~40 languages, but its data froze at about 2021 | Fallback "dictionary" is a frequency floor: `UNSURE`, never `UNKNOWN` (52.3) |
| Dictionaries lack names. TMDB credits give cast and characters ranked by `order` / `episode_count`, in Arabic with `language=ar` | The library gazetteer comes from metadata first, NER (spaCy `xx_ent_wiki_sm`, MIT) second (52.6) |
| LLM correction can fix words no hypothesis had, which is also how it invents words. Confidence-filtered and detect-then-verify designs avoid damaging correct text | *First draft:* a judge that only flags (52.7). *Decided 2026-11-03:* no LLM judge at all; story 52.7 removed |
| Whisper's prompt is cut to 224 tokens; faster-whisper's `hotwords` to 223 and ignored with `prefix` | The derived prompt and hotwords are trimmed to those limits, most-used names first (52.10) |
| Confidence-based selection cut human transcription cost by ~70% against random | The queue orders by calibrated priority; corrections feed the calibration set (52.2, 52.10) |

## Not verified

Otter's and Trint's low-confidence display, Amara's shortcut list (only a third-party guide),
Rev's help page (403), and Subtitle Edit's spell-check / "Fix common errors" details (forum posts
only).

---

## 1. Word timestamps and confidence in the Whisper family

**openai-whisper** (`whisper/transcribe.py`)
- `word_timestamps=False` by default. It gets word times from "the cross-attention pattern and dynamic time warping".
  The `prepend_punctuations` and `append_punctuations` settings attach punctuation to the next or previous word.
- Each word is a dict with the keys `word`, `start`, `end` and `probability`.
- Segment fallback thresholds: `compression_ratio_threshold=2.4`, `logprob_threshold=-1.0`, `no_speech_threshold=0.6`.
  `hallucination_silence_threshold=None` takes effect only when `word_timestamps` is on.
- The built-in hallucination heuristic `word_anomaly_score` adds to a word's score in three cases:
  - +1.0 when `probability < 0.15`
  - a penalty when duration `< 0.133` s
  - a penalty when duration `> 2.0` s
- `is_segment_anomaly` sums the scores of the first 8 non-punctuation words. It flags the segment when the sum is `>= 3`, or when the sum plus 0.01 is at least the word count.
  These rules are a ready-made, in-tree precedent for "suspect word" thresholds.
- `initial_prompt` is documented for "custom vocabularies or proper nouns".
  With `carry_initial_prompt=True`, the prompt is prepended to every window and left-sliced to fit. The budget is `n_text_ctx // 2 - 1`.

**faster-whisper** (`faster_whisper/transcribe.py`)
- `Word(start: float, end: float, word: str, probability: float)`.
- `Segment` fields: `id, seek, start, end, text, tokens, avg_logprob, compression_ratio, no_speech_prob, words: Optional[List[Word]], temperature`.
- Matching thresholds: `log_prob_threshold=-1.0`, `no_speech_threshold=0.6`, `compression_ratio_threshold=2.4`.
- `vad_filter` (Silero) defaults to `False` in `WhisperModel.transcribe` and to True in `BatchedInferencePipeline`.
- `hotwords: Optional[str]` is documented as "Hotwords/hint phrases to provide the model with. Has no effect if prefix is not None."
- Prompt limits: `max_length` is 448. Previous text and hotwords are each cut to `max_length // 2 - 1` = **223 tokens**.
  The prompt plus `max_new_tokens` must fit in 448, or the call raises a `ValueError`.

**mlx-whisper** (PyPI `mlx-whisper`)
- `mlx_whisper.transcribe(path, word_timestamps=True)` gives word-level timestamps.
- The README documents no CLI flag for this; it only points to `mlx_whisper -h`.

**whisperX** (forced alignment, `whisperx/alignment.py`)
- English, French, German, Spanish and Italian use torchaudio wav2vec2 pipelines.
- About 36 other languages use Hugging Face models (`DEFAULT_ALIGN_MODELS_HF`), including:
  - **`ar`: `jonatasgrosman/wav2vec2-large-xlsr-53-arabic`**
  - `fa`: `jonatasgrosman/wav2vec2-large-xlsr-53-persian`
  - `ur`: `kingabzpro/wav2vec2-large-xls-r-300m-Urdu`
  - `he`: `imvladikon/wav2vec2-xls-r-300m-hebrew`
- Each aligned word gets `word`, plus `start`, `end` and `score` (the mean character score, 3 decimals) only when those values are not NaN.
- Words with no alignable characters get interpolated times (`interpolate_method="nearest"`).
- A segment with no characters in the model's dictionary comes back with an empty `words` list.
- Design point: digits and other characters outside the dictionary lose their timing, and a missing `score` is itself a signal worth using.

**Calibration**
- **Huo, Zhang, Tang, ASRU 2025 (arXiv 2509.07195):** Whisper is overconfident in noise. At low SNR, 10–20% of tokens are wrong but carry confidence above 0.7.
  Temperature scaling applied only to the flagged overconfident tokens cut expected calibration error (ECE) by 58% and tripled normalised cross-entropy (NCE) at −18 to −5 dB (R-SPIN dataset).
- **Oneata et al., SLT 2021 (arXiv 2101.05525):** a strong word-confidence baseline combines three steps:
  1. a learned temperature on the logits
  2. negative entropy as the confidence
  3. sum pooling over the word's tokens
- **Aggarwal et al., ICASSP 2025 (arXiv 2502.13446), "C-Whisper":** Whisper is fine-tuned to output a scalar confidence from the audio plus the hypothesis.
  whisper-tiny matches a confidence-estimation-module (CEM) baseline in-domain and beats it on 8 out-of-domain sets. whisper-large beats it "by a substantial margin".
- Implication: treat raw `probability` as a ranking signal. Calibrate per language and model on reviewed data, for example by fitting a temperature or an isotonic map on human-verified words.
  This brief found no Whisper-specific source for isotonic regression; it is the standard alternative in the general calibration literature.

Sources:
- https://raw.githubusercontent.com/openai/whisper/main/whisper/transcribe.py
- https://raw.githubusercontent.com/SYSTRAN/faster-whisper/master/faster_whisper/transcribe.py
- https://github.com/ml-explore/mlx-examples/blob/main/whisper/README.md
- https://raw.githubusercontent.com/m-bain/whisperX/main/whisperx/alignment.py
- https://arxiv.org/abs/2509.07195
- https://arxiv.org/abs/2101.05525
- https://arxiv.org/abs/2502.13446

---

## 2. Detecting out-of-vocabulary (OOV) words

**Hunspell**
- Licence: LGPL/GPL/MPL tri-licence.
- Used by LibreOffice, Firefox, Chrome and macOS.
- Supports "twofold affix stripping" for agglutinative languages, `-s` stemming and `-m` morphological analysis.
  Analysis needs dictionaries that carry morphological fields.

**Python bindings**
- **spylls:** a pure-Python, almost complete Hunspell port, MPL-2.0.
  API: `Dictionary.from_files('en_US')`, `.lookup(w)`, `.suggest(w)`.
- **cyhunspell** (`pip install cyhunspell`): MIT, binds Hunspell 1.7.0.
  API: `Hunspell()`, `spell`, `suggest`, `stem`, and threaded `bulk_suggest`.
  CI badge points at the retired travis-ci.org and the releases page is empty; looks thinly maintained.

**Arabic Hunspell dictionary**
- LibreOffice `dictionaries/ar` ships `ar.aff`, `ar.dic` and `th_ar.dat`.
- `README_ar.txt` says it packages the checker from **the Ayaspell project** (ayaspell.sourceforge.net).
- `COPYING.txt`: "GPL 2.0/LGPL 2.1/MPL 1.1 tri-license".

**Arabic morphology**
A word list alone over-flags Arabic, because clitics and inflections multiply the surface forms.
- **CAMeL Tools**
  - Code is MIT. Python 3.11–3.14, 64-bit. Needs Rust, CMake and Boost.
  - Apple-silicon install: `CMAKE_OSX_ARCHITECTURES=arm64 pip install camel-tools`.
  - Data: `camel_data -i light|defaults|all`, stored in `~/.camel_tools`, or wherever `CAMELTOOLS_DATA` points.
  - **Data licences differ from the code:**
    - `morphology-db-msa-r13` (40.5 MB): GPL v2
    - `morphology-db-egy-r13`: GPL v2
    - `disambig-mle-calima-*`: GPL v2
    - `ner-arabert` (541.6 MB): "AraBERT" licence
    - `dialectid-default`: MIT
  - `Analyzer(db, backoff='NONE'|'NOAN_ALL'|'NOAN_PROP'|'ADD_ALL'|'ADD_PROP', norm_map=None, ...)`.
    The default norm map removes tatweel and maps إ→ا and ى→ي.
  - `analyze(word)` returns a list of analysis dicts.
    The docs do not say what an empty list means; "empty list = no analysis = OOV" needs testing.
- **Farasa (QCRI)**
  - Does segmentation, stemming, POS, NER, diacritisation and lemmatisation, through Java JARs.
  - The `farasapy` wrapper is MIT, but the toolkit itself is "strictly permitted for research purposes only". Commercial use means contacting QCRI.
  - Likely unsuitable for redistribution in a self-hosted product.
- **Qalsadi**
  - GPL; the version is not stated.
  - `Analex.check_text()` returns `StemmedWord`s with `root, type, stem, lemma, vocalized, freq, affix_key`, and more.
  - Also offers `Lemmatizer.lemmatize()`.

**Unicode and Arabic normalisation**
Code points were confirmed against the UCD with Python `unicodedata` and against `Scripts.txt`.
- **Harakat:** U+064B FATHATAN … U+0652 SUKUN, plus U+0653–U+0655 (MADDAH, HAMZA ABOVE, HAMZA BELOW).
  `Scripts.txt`: `064B..0655 ; Inherited`. U+0670 SUPERSCRIPT ALEF is also `Inherited`.
- **Tatweel:** U+0640 is `Common` (category Lm).
- **Alef variants:** آ U+0622, أ U+0623, إ U+0625 and ٱ U+0671 (ALEF WASLA) all map to ا.
- **Hamza seats:** ؤ U+0624, ئ U+0626; bare hamza is U+0621.
- **Teh marbuta and alef maksura:** ة U+0629 ↔ ه U+0647; ى U+0649 ↔ ي U+064A.
- **Persian letters:** ی U+06CC FARSI YEH and ک U+06A9 KEHEH, against Arabic ي U+064A and ك U+0643.
- CAMeL Tools ships `normalize_alef_ar`, `normalize_alef_maksura_ar`, `normalize_teh_marbuta_ar`, and `normalize_unicode(s, compatibility=True)`.
  `normalize_unicode` uses NFKC by default and NFC when `compatibility=False`.
- **UAX #15:** NFKC/NFKD replace compatibility characters such as positional presentation forms; "Both NFD and NFC maintain compatibility composites".
  Normalisation reorders combining marks but does not delete them. Removing diacritics is a separate, language-pack step.

**Segmentation and script detection**
- **UAX #29** (revision 49, Unicode 18.0.0): the default word rules (WB1…WB999) are a tailorable default.
  Thai, Lao, Chinese and Japanese "require the use of dictionary lookup".
- **ICU BreakIterator:** offers character, word, line and sentence boundaries.
  `getRuleStatus()` tells number, letter, kana and ideograph words apart from non-words (`UBRK_WORD_NONE`).
  Dictionary breaking is built in for Chinese, Japanese, Thai, Lao, Khmer and Burmese.
- **UAX #24** (revision 41): every code point has a `Script` value; `Script_Extensions` lists the scripts a character is used with.
  Resolve `Common` and `Inherited` from context before you test for mixed script. This is the basis for detecting code-switching, for example a Latin word inside Arabic text.

**wordfreq**
- Code is Apache-2.0. Data is CC BY-SA 4.0, with extra attribution terms for the Google Books, OpenSubtitles and SUBTLEX sources.
- Arabic is supported, with a "large" list built from 5 sources.
- API: `word_frequency`, `zipf_frequency`, `tokenize`, `top_n_list`.
  Arabic tokenisation "normalizes ligatures and removes combining marks".
- **Sunset (September 2024):** the data is a snapshot through about 2021 and will not be updated. Reasons given: AI-generated text polluting web data, and the loss of Twitter and Reddit access.
  A low Zipf score is a cheap, any-language rarity signal, but it ages.

Sources:
- https://github.com/hunspell/hunspell
- https://github.com/zverok/spylls
- https://github.com/MSeal/cython_hunspell
- https://github.com/LibreOffice/dictionaries/tree/master/ar
- https://raw.githubusercontent.com/LibreOffice/dictionaries/master/ar/README_ar.txt
- https://raw.githubusercontent.com/LibreOffice/dictionaries/master/ar/COPYING.txt
- https://github.com/CAMeL-Lab/camel_tools
- https://camel-tools.readthedocs.io/en/latest/reference/packages.html
- https://camel-tools.readthedocs.io/en/latest/api/morphology/analyzer.html
- https://camel-tools.readthedocs.io/en/latest/api/utils/normalize.html
- https://pypi.org/project/farasapy/
- https://github.com/linuxscout/qalsadi
- https://www.unicode.org/Public/UCD/latest/ucd/Scripts.txt
- https://unicode.org/reports/tr15/
- https://www.unicode.org/reports/tr29/
- https://www.unicode.org/reports/tr24/
- https://unicode-org.github.io/icu/userguide/boundaryanalysis/
- https://github.com/rspeer/wordfreq
- https://github.com/rspeer/wordfreq/blob/master/SUNSET.md

---

## 3. Named entities

Spell dictionaries do not contain cast or character names, so without a names allowlist the OOV check floods the review queue with false positives.

**NER options**
- **spaCy `xx_ent_wiki_sm` 3.8.0:** trained on WikiNER, with the labels LOC, MISC, ORG and PER.
  MIT, 10 MB, entity P/R/F 83.57/82.71/83.14.
- **Stanza:** NER for 33 languages.
  - Arabic: AQMAR, 4 types, F1 74.3.
  - Persian: Arman, 6 types, F1 80.07.
- **CAMeL `NERecognizer`:** `NERecognizer.pretrained()`, where `'arabert'` is the only model.
  - `predict_sentence(tokens)` and `predict(sentences, batch_size=32)`.
  - Input must be pre-tokenised by whitespace and punctuation.
  - Data package `ner-arabert` is 541.6 MB under the AraBERT licence.

**Media metadata as a gazetteer (TMDB)**
- Films: `GET /3/movie/{movie_id}/credits`.
  - Cast items: `name, original_name, character, known_for_department, order, cast_id, credit_id`.
  - Crew items: `department, job`.
- Series: `GET /3/tv/{series_id}/aggregate_credits`.
  - Cast items: `roles[]` of `{credit_id, character, episode_count}`, plus `total_episode_count`.
- Both take `language` (default `en-US`). Fetch with `language=ar` to get Arabic spellings of names.
- Use `order` and `episode_count` to rank, so main characters go into the hotword and prompt budget first.

Sources:
- https://spacy.io/models/xx
- https://github.com/explosion/spacy-models/releases/tag/xx_ent_wiki_sm-3.8.0
- https://stanfordnlp.github.io/stanza/ner_models.html
- https://camel-tools.readthedocs.io/en/latest/api/ner.html
- https://developer.themoviedb.org/reference/movie-credits
- https://developer.themoviedb.org/reference/tv-series-aggregate-credits

---

## 4. How review tools present low-confidence words

**Confidence in cloud speech-to-text APIs**
- **AWS Transcribe:** `results.items[]` has `id`, `start_time`, `end_time`, `type` (`pronunciation` | `punctuation`) and `alternatives[{confidence, content}]`.
  Confidence values are strings. In the example, punctuation has no times and carries `"confidence": "0.0"`.
- **Google Speech-to-Text:** `enableWordConfidence` (REST) or `enable_word_confidence` gives `words[].confidence` next to `startOffset` and `endOffset`, on a 0.0–1.0 scale.
  The docs mark the feature Pre-GA.

**Editor presentation**
- **Rev:** low-confidence highlighting is an editor option. The blog describes "the ability to look at low confidence words".
  Rev's help-centre article returned 403 and could not be fetched.
- **Auphonic** (2024 editor): "Highlight Low Confidence" marks uncertain spans and shows the confidence value on hover.
  "Play on Click" starts playback at any clicked word, and a playback slider highlights the current word.
- **Echo360:** a Word Confidence slider (0–100) underlines every word below the threshold.
  The default threshold is the transcript's mean confidence.
  "Word Confidence is not the same as accuracy". Editing a word clears the confidence scores for its whole cue.
- **Sonix:** a thermometer icon toggles a heatmap in which lighter words have lower confidence; there is also File > Transcript Quality.
- **Descript:** a "transcription glossary" for uncommon, technical and often-misspelled words. A help page on low-confidence display was not found.
- **Subtitle Edit:**
  - `{language}_OCRFixReplaceList.xml` files in the Dictionaries folder drive automatic fixes, for example `eng_OCRFixReplaceList.xml`.
  - The OCR "Unknown words" list offers three actions: take a suggestion, type a correction, or add the word to the fix dictionary, with Enter jumping to the line.
  - Options → Shortcuts configures every command.
  - Spell check with Hunspell/OpenOffice dictionaries, a names list and "Fix common errors" appears only in forum posts and release notes, not in the fetched official page.
- **oTranscribe** (MIT): no speech recognition. Keyboard-only playback: Esc pauses, F1 rewinds, F2 fast-forwards, F3/F4 change speed. Autosaves to `localStorage` every second.
- **Amara:** shortcuts are listed in-editor ("See more shortcuts"). Shift+Tab = rewind 2 s comes from a third-party guide only.

**Evidence from a user study**
- **Kuhn, Kersken, Zimmermann, CHI EA 2025 (arXiv 2503.15124), 36 participants:** confidence correlates with accuracy, but error detection misses errors or yields many false positives.
  Confidence highlighting "neither improved correction efficiency nor was perceived as helpful".
- Design implication: a review queue built from several signals (OOV, LLM disagreement, NER, confidence), each with its own reason label, should do better than a confidence colour wash alone.

**Not verified:** Otter's and Trint's low-confidence display. Trint's public docs only describe highlighting synced to playback.

Sources:
- https://docs.aws.amazon.com/transcribe/latest/dg/how-input.html
- https://docs.cloud.google.com/speech-to-text/docs/word-confidence
- https://www.rev.com/blog/transcript-editor-overview
- https://auphonic.com/blog/2024/03/21/new-auphonic-transcript-editor/
- https://support.echo360.com/hc/en-us/articles/11077346298381
- https://help.sonix.ai/en/articles/1756215-can-sonix-show-me-which-words-it-thinks-are-incorrect
- https://feedback.descript.com/changelog/transcription-glossary
- https://niksedk.github.io/subtitleedit-avalonia/features/ocr.html
- https://niksedk.github.io/subtitleedit-avalonia/features/shortcuts.html
- https://github.com/oTranscribe/oTranscribe
- https://arxiv.org/abs/2503.15124

---

## 5. An LLM that flags words instead of rewriting them

- **HyPoradise** (Chen et al., NeurIPS 2023 Datasets and Benchmarks, arXiv 2309.15701): more than 334K pairs of N-best hypotheses and their transcripts.
  LLM "generative error correction" beats the re-ranking upper bound and "can even correct those tokens that are missing in N-best list".
  That is both the power and the risk: the model can invent words.
- **Naderi et al., Interspeech 2024 (arXiv 2407.21414):** confidence-based filtering decides which transcripts the LLM may touch, so it does not introduce errors into accurate ones.
  The gains go mainly to weaker ASR systems.
- **Fang et al., ASRU (arXiv 2505.24347), RLLM-CF:** three stages:
  1. error pre-detection
  2. chain-of-thought iterative correction
  3. verification of the reasoning
  Built because direct prompting hallucinates edits to correct text. GPT-4o with the framework cut CER/WER by a relative 21%, 11%, 9% and 11.4% on AISHELL-1/2 and LibriSpeech.
- **Gu et al., arXiv 2405.15216, rev. March 2026:** a compact specialised seq2seq corrector with 15× fewer parameters (1.5% / 3.3% WER on LibriSpeech clean/other) beats LLMs "in the low-error regime where LLMs struggle".
  The authors cite hallucination and latency as the LLM drawbacks.
- **Arabic:** Talafha et al., arXiv 2511.18774: context-aware prompting of Whisper with no retraining. The decoder is prompted with first-pass hypotheses and retrieved exemplars.
  Relative WER drops 22.29% on MSA and 9.15% on dialectal speech across 10 conditions.
- **Structured flags:** Ollama structured outputs (Dec 2024) take a JSON Schema in `format`, for example from Pydantic `model_json_schema()`, and recommend temperature 0.
  Use this so the reviewer returns `[{word_index, suspect_reason, suggestion, confidence}]` and never a rewritten transcript.
  The pipeline then applies nothing automatically, which matches the "pre-detection, then verify" lesson above.

Sources:
- https://arxiv.org/abs/2309.15701
- https://arxiv.org/abs/2407.21414
- https://arxiv.org/abs/2505.24347
- https://arxiv.org/abs/2405.15216
- https://arxiv.org/abs/2511.18774
- https://ollama.com/blog/structured-outputs

---

## 6. Learning from corrections: custom vocabulary and active learning

**Prompting and hotwords**
- **Whisper prompt** (OpenAI cookbook): "limited to only 224 tokens", and anything before the final 224 is ignored.
  The prompt steers style; it is not followed as an instruction. Putting correct spellings of rare names in it helps.
- **faster-whisper:** `hotwords` and previous text are each capped at 223 tokens (`448 // 2 - 1`). `hotwords` is ignored when `prefix` is set.
- **openai-whisper:** `carry_initial_prompt=True` repeats the prompt in every 30 s window, so names stay in context for the whole file.

**Biasing research**
- **Liu, Peng, Chng, APSIPA 2025 (arXiv 2508.17796):** zero-shot trie biasing on a frozen Whisper, using TTS pronunciation variants and shallow fusion in beam search.
  Biased-word error rate (B-WER) falls about 43–44% on LibriSpeech with unbiased WER essentially unchanged.
  Corrected names from the review queue are the natural input for this.

**Cloud equivalents**
- **AWS Transcribe custom vocabulary:**
  - Up to 100 per account, 50 KB each, entries of at most 256 characters.
  - Table format (`Phrase`, `DisplayAs`, …) is preferred because the list format is deprecated.
  - Only characters from the language's character set are allowed.
- **Google speech adaptation:** a `PhraseSet` holds phrases with an optional boost, a float above 0 whose "practical maximum" is 20.
  Higher boost means fewer false negatives and more false positives. `CustomClass`/`ClassItem` define lists such as names.

**Active learning**
- **Drugman, Pylkkönen, Kneser, Interspeech 2016 (arXiv 1903.02852):** confidence-based selection for human transcription cut transcription cost by about 70% against random selection, and improved WER by about 12.5% relative at a fixed budget.
  More sophisticated confidence models gave no extra selection gain. Semi-supervised training helps early, but its gains fade once active learning starts.
- Implication: rank queue items by (calibrated) low confidence, then feed confirmed corrections into three places:
  1. the per-title or per-language names list (stops repeat OOV flags)
  2. the hotword and prompt budget
  3. the calibration set for that language pack

Sources:
- https://developers.openai.com/cookbook/examples/whisper_prompting_guide
- https://raw.githubusercontent.com/SYSTRAN/faster-whisper/master/faster_whisper/transcribe.py
- https://arxiv.org/abs/2508.17796
- https://docs.aws.amazon.com/transcribe/latest/dg/custom-vocabulary.html
- https://docs.cloud.google.com/speech-to-text/docs/adaptation-model
- https://arxiv.org/abs/1903.02852
