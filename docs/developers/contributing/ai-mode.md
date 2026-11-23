# AI mode: asking the home a question

`POST /api/search/ai` answers a question from the passages of the caller's libraries. The home's
language model reads the best passages and writes a short answer in the question's language,
each sentence citing the passages it comes from. This guide is for people who write a client:
how to ask, how to read the stream, and what to show when there is no answer.

The request and the codes are in the [API reference](https://maktaba.dev/api/), under
**Search** (`searchAI`).

## Asking

```json
{"q": "What are the kinds of patience?",
 "history": [{"q": "What is patience?", "answer": "Holding oneself back from despair."}],
 "filters": {"language": ["ar"]}}
```

- `q` is the question (500 characters at most).
- `history` is the conversation so far, oldest first. The last three turns go to the model, so a
  follow-up can say "and its kinds?". The passages are found for `q` alone.
- `filters` are a search's filters. The libraries are always the caller's own.

## Reading the stream

The answer is Server-Sent Events. Read them in order:

| Event | Data | What to do |
|---|---|---|
| `queue` | `{"position": 2}` | Another answer is being written. Show the place; it changes as the line moves. |
| `sources` | `{"q", "passages": [{"n", "video_id", "title", "library_name", "poster_url", "start_sec", "end_sec", "text"}]}` | Show the passages at once: there is something to read while the answer is written. |
| `text` | `{"text", "citations": [{"n", "video_id", "start_sec", "end_sec", "quote"}]}` | One sentence. Append it, with a link for each citation that opens the player at `start_sec`. |
| `done` | `{"answered", "reason", "sentences", "dropped", "first_token_ms", "took_ms"}` | The end. When `answered` is false, say the passages do not answer the question and keep them shown. |
| `error` | `{"code"}` | The answer stopped. The sentences already shown stand. |

Every `text` event cites at least one passage of the `sources` event, by its `n`. The home drops
a sentence that cites nothing, or only a passage it did not give, before it is sent, and counts
it in `dropped`. A client never has to check a citation itself.

`reason` is `no-passages` (the search found nothing the caller may see; the model was not
asked), `not-in-passages` (the model found no answer in them) or `timeout` (the answer took
longer than the home allows; the sentences sent stand).

A comment line (`: ping`) arrives every 15 seconds while the question waits or the model reads.
Close the connection to cancel: the home stops the model and gives the turn to the next question.

## When there is no answer to write

| Status | `code` | Show |
|---|---|---|
| 409 | `ai-unavailable` | This home has no language model, or it is switched off. `help.settings` and `help.help` are paths in the web app: where an admin sets a model server, and the help page. |
| 409 | `ai-off` | An admin turned AI mode off. |
| 429 | `ai-busy` | Eight questions are waiting already. `position` is the place this one would have had; ask again after `Retry-After` seconds. |

## How the home answers

- The passages are those of the same hybrid search as `POST /api/search`, under the caller's
  libraries and parental limits. When the question's own words find too few, each of its content
  words is searched too, and the videos that match more of them come first.
- The best passages of the best ten videos (three at most from one video, 30 in all), each with
  the line before and after it, are numbered and given to the model with the question.
- The model is told to answer only from them and to cite each sentence like `[2]`. Arabic-Indic
  digits and lists like `[1, 3]` are read too. A sentence cites five passages at most.
- One answer is written at a time per home; up to eight questions wait in line.

The admin's settings are **Settings → Rules → Search** (AI mode on or off, the passages it
reads, its model, the longest answer). The model server is the tag check's.

## Testing a client

The home's tests ask a fake model (`api/internal/handlers/search/ai_test.go`). To try a real one,
`TestAI_RealModel` asks an Ollama server:

```sh
MAKTABA_TEST_OLLAMA_URL=http://127.0.0.1:11434 go test ./internal/handlers/search/ -run TestAI_RealModel -v
```
