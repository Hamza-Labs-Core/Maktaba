# Word review

**Word review** is where a home admin checks the transcript words that may be wrong, one at a time, hears them, and fixes them.

## What it is {#what-it-is}

After a video is transcribed, the **Word review** step ([stage](/help/stages#word-check)) looks at every word: the ones the language's dictionary does not know, the ones Whisper was unsure of, the ones the second listen heard differently, and the words of a Quran verse that differ from the Quran text. It saves where each one is, with the reasons and suggested spellings. Nothing is changed by itself: a person decides.

A fix changes the transcript, its subtitle files and search at once, without transcribing again, and can be undone. Every fix teaches: a name or a dictionary word is never asked about again, and new transcriptions are given the names. Only home admins review words.

## How to use it {#how-to-use}

- The word with the highest priority opens with its sentence, and its audio plays. **Space** plays and pauses, **R** replays.
- Press **1** to **5** to take a suggestion, **E** to type the spelling, **Enter** to keep it as it is, **N** for a name, **D** to add it to the dictionary, **S** to skip. The next word opens by itself.
- **A** applies the same action to every other place the word is flagged, as one batch. **U** undoes the last action, the whole batch at once.
- Press **?** for every shortcut. The keys work with an Arabic keyboard too.
- In a video's **Transcript** tab, **Fix words** lets you select any word and fix it there.
- Each library's settings (in Manage libraries) say how many words an hour of audio may ask about, how sensitive the check is, and which reasons count.

## What the states and messages mean {#states}

- **Being reviewed by …**: another admin has the word open; **Take over** to resolve it anyway.
- **The sentence changed**: someone fixed a word nearby since; the word is shown again as it is now.
- **Differs from the Quran text**: the word is part of a recited or quoted verse and differs from the verse. The verse's word is the first suggestion. Quran text: Tanzil Project (tanzil.net).
- **waiting (transcription busy)**: older transcripts are checked only while nothing waits to be transcribed.

## FAQ {#faq}

**Does a fix transcribe the video again?** No. It edits the text, the subtitle files follow within a minute, and search at once.

**What if the video is transcribed again later?** The fixes are carried over; one that cannot be placed comes back as a word to review.

**Can a library's editors review?** No. Only home admins review words.

## When something goes wrong {#troubleshooting}

- **Audio not available**: the file is offline; the actions still work.
- **No words to review** while you expect some: check the library's word review settings and the [language packs](/review/languages).
- **Too many words**: lower the sensitivity or the most words per hour in the library's settings.
