# Decisions for the owner

Every decision an agent needs from the owner goes through one inbox, whatever the agent's
provider (Claude, Codex, GLM, Kimi or any other). The owner answers all of them on one page, a
claude.ai artifact named **Decisions**, pinned in the owner's sidebar. Nothing has to be shared
or re-asked in chat.

## How an agent asks

1. Write a request as JSON:

   ```json
   {
     "id": "apple-cast",
     "title": "Google Cast is built in but switched off",
     "context": "Why it matters, the facts, links. Plain text, short paragraphs, no secrets.",
     "options": [
       {"id": "leave-out", "label": "Leave Cast out of Apple builds", "recommended": true},
       {"id": "enable", "label": "Turn Cast on now", "detail": "Optional one-line detail."}
     ],
     "allow_other": true,
     "images": [{"path": "/abs/path/shot-en.png", "caption": "English"}],
     "from": "Agent or thread name, with its link",
     "project": "Maktaba",
     "group": "Optional heading for related decisions"
   }
   ```

   Exactly one option is recommended. Images are PNG, JPEG or WebP: dark theme, English and
   Arabic pairs, and any demo media shows no women or girls.
2. Run `~/.maktaba/decisions/bin/ask request.json`. It checks the request and prints its id.
3. Write `DECISION NEEDED: <id> — <title>` in your report, and keep working on anything that
   doesn't depend on the answer.
4. Read the answer with `~/.maktaba/decisions/bin/answer <id>`: the answer's JSON (`choice`,
   `label`, `note`, `answeredAt`), or `pending` with exit code 3. `--wait SECONDS` polls once a
   minute. To withdraw a request, set its `status` to `withdrawn` in
   `~/.maktaba/decisions/requests/<id>.json`.

## How it reaches the page

A scheduled session syncs the inbox with the page every 15 minutes: it uploads new requests and
their images, and writes each answer back to `~/.maktaba/decisions/answers/<id>.json`. Any Claude
session with the Artifact tools may run the same steps (`~/.maktaba/decisions/README.md`). When
a sync can't run, it writes `~/.maktaba/decisions/SYNC-FAILED.txt`.

## What not to do

- Don't ask the owner to decide in chat, in a pull request or issue, or on a page of your own.
- Don't recommend more than one option, and don't leave out the context the owner needs.
- Don't quote the owner's answer in repo text: state the decision neutrally (`CLAUDE.md` rule 5).
