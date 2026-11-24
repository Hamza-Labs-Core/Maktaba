---
schema: 1
kind: pr
pr: 1580
title: "The store reviewers' home carries the demo library's films, with no woman or girl in any picture"
date: 2026-11-24
session: a0368eb0-9e83-4dbf-8f4e-e64674284368
agent: claude-code
prs: [1580, 1565]
---

## What happened
- **The old content broke the demo-content rule.** The review home's content set (`deploy/review-home/content/manifest.json`) carried Spring, Sintel and Cosmos Laundromat. #1565 recorded those three as an open item. A look through the whole set found two more:
  - Netflix's Meridian, the 4K HDR10 test file, has women in it;
  - the LibriVox Arabian Nights audiobook was read by five readers and is the stories of a heroine.
- **#1580 replaces them** with the films of #1565's demo library that fit the home's licence list (public domain or CC BY), keeping each test path:
  - 4K HEVC HDR10 from NASA's 4K Moon-phase visualization;
  - 1080p H.264 from Caminandes: Llama Drama;
  - MKV with EAC3 and English and Arabic WebVTT from Curiosity: Landing on Mars;
  - Big Buck Bunny stays as the file that needs a transcode.
- **The audiobook** is now al-Mawardi's أدب الدنيا والدين, read in Arabic by one reader.
- **Captions:** the Curiosity animation has no narration, so its 56 captions describe the picture. They were written from a 12-second contact sheet of the whole film, with the Arabic cue for cue with the English (the test wants more than 50 cues, matched).

## What we learned
- **Content check:** a content set has to be checked whole. The PR's open item named three films, and two more failed the same check.
- **Licence list:** the review home accepts only public domain and CC BY 3.0/4.0, so two of the demo library's titles (CC BY 2.0, CC BY-SA 3.0) can't go there.
- **Commons checksums:** Wikimedia Commons publishes each file's size and SHA-1 (`prop=imageinfo&iiprop=size|sha1`), so a Commons source can be verified by `content.py` just like an Internet Archive one.
- **Old files:** a content change leaves the files it drops in the review home's media folder until someone removes them. The README now says so.

## Keep doing
- Proving each ffmpeg derive locally before pushing: `content.py fetch --only …` for the small items, and the 4K arguments on a short clip (`-t 3`).

## Stop doing
- Picking test media by format and licence alone, without looking at who is in it.

## Open
- **The live review home** needs `setup.sh` and `seed.sh seed` run after #1580 merges, and the six dropped files removed by hand (listed in #1580). That is a step on the machine, for whoever runs it.
