# LLMs across the cluster

Any machine in the cluster can run a local LLM — with Ollama, LM Studio or another
OpenAI-compatible server — and every machine can use it. Maktaba finds the models on each
machine, and when something needs a model, the home sends the request to a machine that
has it. Your own tools can use all of the cluster's models through one address on the home.

## How it works

1. You run an LLM server on a machine (for example a PC with a good GPU), listening only on
   that machine (`127.0.0.1`). It is not opened to the network.
2. The Maktaba worker on that machine finds the server, lists its models, and tells the
   home. It checks again every 30 seconds.
3. When Maktaba on any machine — or your tool, through the home — asks for a model, the
   home picks a machine that has it: one where the model is already loaded first, then the
   least busy, then the asking machine itself. The request and the answer travel over the
   machines' own encrypted connection to the home.
4. If every machine with that model is busy, the request waits its turn (first come, first
   served). If none has the model, it fails at once.

Each server runs one request at a time by default; raise `MAKTABA_LLM_MAX_PARALLEL` on that
machine if your hardware can take more.

**Admin > Machines** shows each machine's LLM servers and models, which are loaded, and the
requests running.

## Run Ollama on a machine

1. Install Ollama from <https://ollama.com/download> (macOS, Linux, Windows).
2. Download a model, for example:

   ```sh
   ollama pull qwen2.5:7b
   ```

3. That is all. Ollama listens on `http://127.0.0.1:11434` by default, which is where the
   Maktaba worker looks. Leave it there — there is no need to open Ollama to the network.

If Ollama runs somewhere else on that machine, set `MAKTABA_OLLAMA_URL` for the Maktaba
worker (in its service settings), for example `MAKTABA_OLLAMA_URL=http://127.0.0.1:11500`.

## Run LM Studio (or another OpenAI-compatible server) on a machine

1. In LM Studio, download a model, then start its local server (the **Developer** tab,
   **Start server**). It listens on `http://127.0.0.1:1234` by default.
2. Tell the Maktaba worker on that machine where it is, in the worker's environment (its
   service settings):

   ```sh
   MAKTABA_LLM_ENDPOINTS=http://127.0.0.1:1234/v1
   ```

3. Restart the worker.

The same works for vLLM, the llama.cpp server and other servers that speak the OpenAI API:
list their base addresses in `MAKTABA_LLM_ENDPOINTS`, separated by commas.

## Use the cluster's models from your own tools

The home serves an OpenAI-compatible address:

```
http://<home>:8080/api/cluster/llm/v1
```

Any tool that lets you set an "OpenAI base URL" can use it. It needs a key: create a
**personal access token** in the web app (your profile, **API tokens**), and use it as the
API key. The token is sent as `Authorization: Bearer <token>`.

List the models (from every machine):

```sh
export MAKTABA_TOKEN=pat_...        # your personal access token
curl -s http://192.168.50.20:8080/api/cluster/llm/v1/models \
  -H "Authorization: Bearer $MAKTABA_TOKEN"
```

Chat, streaming the answer:

```sh
curl -N http://192.168.50.20:8080/api/cluster/llm/v1/chat/completions \
  -H "Authorization: Bearer $MAKTABA_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
        "model": "qwen2.5:7b",
        "stream": true,
        "messages": [{"role": "user", "content": "Summarise the plot of Kalila and Dimna in two sentences."}]
      }'
```

Embeddings:

```sh
curl -s http://192.168.50.20:8080/api/cluster/llm/v1/embeddings \
  -H "Authorization: Bearer $MAKTABA_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"model": "nomic-embed-text", "input": ["first text", "second text"]}'
```

With the OpenAI Python library:

```python
from openai import OpenAI

client = OpenAI(base_url="http://192.168.50.20:8080/api/cluster/llm/v1", api_key="pat_...")
reply = client.chat.completions.create(
    model="qwen2.5:7b",
    messages=[{"role": "user", "content": "Hello"}],
)
print(reply.choices[0].message.content)
```

Use the model names exactly as `/models` lists them.

## When a request fails

| Error | HTTP | Meaning | What to do |
|---|---|---|---|
| `no_model` | 404 | No machine has this model. | Check the name with `/models`; pull the model on a machine. |
| `no_capacity` | 503 | Every machine with the model stayed busy for 5 minutes. | Try again later, add a machine with the model, or raise `MAKTABA_LLM_MAX_PARALLEL` where the hardware allows. |
| `machine_gone` | 502 | The machine running the request disconnected. | Try again; another machine may take it. |
| `runtime_error` | 502 | The LLM server on that machine returned an error. | Look at that machine's LLM server and its log. |
| — | 401 | Missing or wrong token. | Use a valid personal access token. |

Inside Maktaba, a machine whose connection to the home is down can still use its own
models, but not other machines' (`no_channel`).

## Privacy

Prompts and answers are never saved — not on the machines, not on the home, not on the
NAS. Each machine records only counts for each request it ran (model, who asked, tokens,
time, error) in its `llm` files on the share (see [Files on the share](files-on-the-share.md)).

## Settings

On a machine running a model (in the worker's environment):

| Setting | Default | Meaning |
|---|---|---|
| `MAKTABA_OLLAMA_URL` | `http://127.0.0.1:11434` | where Ollama listens |
| `MAKTABA_LLM_ENDPOINTS` | empty | other OpenAI-compatible servers, comma-separated |
| `MAKTABA_LLM_MAX_PARALLEL` | `1` | requests at once per server |

On the home:

| Setting | Default | Meaning |
|---|---|---|
| `MAKTABA_LLM_QUEUE_TIMEOUT_S` | `300` | how long a request may wait for a free machine |
