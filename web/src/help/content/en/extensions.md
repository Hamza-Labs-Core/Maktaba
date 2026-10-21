# Extensions

Connect Maktaba to the other services it can work with: Plex, dizqueTV, Cloudflare Stream and web search. Switch each on or off, fill in its settings and test the connection.

## What it is {#what-it-is}

Maktaba works on its own. Extensions are optional add-ons that let it use another service:

- **Plex**: keeps Plex collections in step with the seasons Maktaba finds, and puts Maktaba's channels in Plex Live TV. See [Plex](/help/plex).
- **dizqueTV**: writes the planned channels into a dizqueTV server and checks what they air. See [dizqueTV](/help/dizquetv).
- **Cloudflare Stream**: lets people watch the channels away from home and powers simulcast. See [Watching away from home](/help/remote-tv).
- **Web search**: lets **Find metadata** search the web for a video. See [Metadata](/help/metadata).

The **Extensions** page at [/admin/extensions](/admin/extensions) has one card per extension. Only admins can open it. Tokens and keys you enter are stored encrypted on your home server and never shown again.

## How to use it {#how-to-use}

### Connect an extension {#connect}

1. Open **Extensions**.
2. On the extension's card, fill in its settings. Fields marked **Required** must be filled.
3. Click **Save settings**. The card then says **Configured** once every required field is set.
4. Click **Test connection** and read the result (see [Checks](#checks)).
5. Switch on **Enabled**.

A saved secret field shows dots and **Saved. Type to replace it.** Leave it empty to keep the saved value, or type a new one and save. **Not set** means nothing is saved yet.

### Switch an extension on or off {#switch}

The **Enabled** switch at the top right of each card turns the extension on or off at once. While an extension is off, its steps in [Processing](/queue) wait with **Switched off**; while it is on but not configured, they wait with **Not set up**. Nothing is lost: the steps run when you switch it back on. See [Processing](/help/processing#waiting-disabled).

### Plex {#plex}

Fields: **Plex URL**, **Plex token** and an optional **Path map** (how Plex sees the paths of your files). The easiest way is **Sign in with Plex**:

1. Click **Sign in with Plex**. A Plex window opens; sign in there. If no window opens, use the link shown.
2. Back in Maktaba, under **Choose the Plex server to use**, pick a server and click **Use this server**.
3. Maktaba saves the address and token and says **Connected to Plex server** with its name.

Then run **Test connection**. More in [Plex](/help/plex).

### dizqueTV {#dizquetv}

One field: **dizqueTV URL**, the address of your dizqueTV server. dizqueTV plays from Plex, so the Plex extension must be set up too. More in [dizqueTV](/help/dizquetv).

### Cloudflare Stream {#cloudflare}

Fields: **Account ID**, **API token** and an optional customer subdomain code that **Test connection** fills in. This card also shows the remote viewing status, the **Remote viewing** switch, **Remove integration** and the use and limits box. All of it is explained in [Watching away from home](/help/remote-tv).

### Web search {#web-search}

Fields: a **Google API key** with a **Search engine ID (cx)** for Google Programmable Search, and an optional **Ollama API key** for Ollama's web search. Without them, **Find metadata** suggests from Maktaba's own information only. See [Metadata](/help/metadata).

### The extension relay {#relay}

Your Plex token stays on your home server. When another machine of yours runs the Plex steps, it sends its Plex calls through the home, which adds the token, so the token never leaves the home. This is the **Extension relay**, on by default. In [Settings → Rules](/settings/rules), section **Extension relay**, you can switch it off (then those steps run on the home only) or set limits per machine: calls a minute, burst, calls at once, largest answer and time limit per call. See [the rules reference](/help/rules#relay).

## Checks {#checks}

**Test connection** shows **Connection works:** or **Connection failed:** with a short message, then one line per check. Each line is **ok**, **failed**, **missing, optional** (a problem that does not stop the extension) or **not checked** (skipped because an earlier check failed), with the reason. A failed line may end with a short code, for example timeout or token_rejected.

### Plex checks {#plex-checks}

- **Server reachable**: Maktaba reached the Plex server and it answered as Plex.
- **Plex token**: Plex accepted the token; the line shows how many libraries it sees.
- **Server**: the server's name and version (optional).
- **Live TV**: Live TV is available on the server and how many DVRs it has. It needs Plex Pass on the server owner's account (optional).
- **Maktaba's tuner in Plex**: whether Maktaba's tuner is added to Plex Live TV (optional).
- **dizqueTV's tuner in Plex**: whether a dizqueTV tuner is there too (optional, for information).
- **Path map**: whether your library's folders are folders of a Plex library, using the path map (optional).

### dizqueTV checks {#dizquetv-checks}

- **Server reachable**: Maktaba reached dizqueTV and it answered as dizqueTV, with its version.
- **Channels**: how many channels dizqueTV has.
- **Plex servers in dizqueTV**: the Plex servers registered in dizqueTV, and whether one is the same Plex Maktaba uses (optional).

### Cloudflare Stream checks {#cloudflare-checks}

Listed in [Watching away from home](/help/remote-tv#checks).

### Web search checks {#web-search-checks}

- **Extension on**, **Google Programmable Search**, **Ollama web search** and **Web search**: whether the extension is on, whether each search service answers with the saved keys (Ollama is optional), and whether at least one of them answers.

## What the states and messages mean {#states}

- **Configured**: every required setting is filled in.
- **Not configured**: a required setting is missing.
- **Enabled**: the switch is on; Maktaba uses this extension.
- **version …**: the extension's version.
- **Settings saved.**: your changes are stored.
- **Connection works: …** / **Connection failed: …**: the result of **Test connection**; the lines below give the detail.
- **Waiting for you to sign in to Plex in the new window…**: finish signing in to Plex in the other window.
- **Connected to Plex server …**: the Plex sign-in worked and the server is saved.

## FAQ {#faq}

### Do I need any extension? {#faq-needed}

No. Maktaba scans, transcribes, organises and runs its own channels without them. Add one only for the service you use.

### Can I see a saved token? {#faq-see-token}

No. Saved tokens and keys are never shown again. Type a new one to replace it.

### What happens to work in progress when I switch an extension off? {#faq-switch-off}

Its steps wait with **Switched off** and run when you switch it back on. Nothing else in Maktaba stops.

### Does the Plex token reach my other machines? {#faq-relay}

No. With the extension relay on, the home adds the token to their Plex calls; with it off, the Plex steps run on the home only.

## When something goes wrong {#troubleshooting}

### Could not load the extensions. {#load-error}

- **What it means:** the page could not read the extensions from the server.
- **Does Maktaba fix it itself?** No.
- **What you can do:** reload the page. If it keeps happening, check [System health](/help/system-health).

### Server not reachable {#unreachable}

- **What it means:** Maktaba could not reach the address you entered. The reason says why: no answer in time, connection refused, the name does not resolve, or a certificate problem.
- **Does Maktaba fix it itself?** No. Steps that need the service retry by themselves later.
- **What you can do:** check the address and port, that the service is running, and that this server can reach it on the network. For a certificate problem, use the address the reason suggests (for Plex, its plex.direct address, or http:// on your local network).

### The server redirected to another host {#redirect}

- **What it means:** the address sends Maktaba somewhere else, and Maktaba does not follow it.
- **Does Maktaba fix it itself?** No.
- **What you can do:** enter the final address instead.

### Token rejected {#token-rejected}

- **What it means:** the service refused the saved token or key.
- **Does Maktaba fix it itself?** No.
- **What you can do:** for Plex, click **Sign in with Plex** again. For the others, paste a new token or key, save and test again.

### Answered, but not as the expected service {#bad-answer}

- **What it means:** something answered at that address, but it is not Plex or dizqueTV.
- **Does Maktaba fix it itself?** No.
- **What you can do:** check the port and the address. Plex usually listens on 32400 and dizqueTV on 8000.

### Plex sign-in timed out. Try again. {#plex-signin-timeout}

- **What it means:** the Plex sign-in was not finished in time.
- **Does Maktaba fix it itself?** No.
- **What you can do:** click **Sign in with Plex** again and finish signing in the Plex window.

### Signed in, but this Plex account has no reachable servers. {#plex-no-servers}

- **What it means:** the Plex account has no server Maktaba can reach.
- **Does Maktaba fix it itself?** No.
- **What you can do:** check that your Plex server is running and signed in to the same account, or enter **Plex URL** by hand.
