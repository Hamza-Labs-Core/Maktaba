# Settings

The Settings page: your account, sessions, appearance, privacy, AI models, rules, experiments, storage, subscription and version.

## What it is {#what-it-is}

[Settings](/settings) gathers the choices for you and for the whole server. It has nine tabs: **Account**, **Sessions**, **Appearance**, **Privacy**, **Models**, **Rules**, **Advanced**, **Subscription** and **About**. Admins see a tenth, **Storage & discovery**, after **Advanced**. Everyone else can open every tab; changes that affect the whole server are for admins only.

## How to use it {#how-to-use}

### Account {#account-tab}

- **Signed in as** shows your username, with **(admin)** if you are one.
- **Sign out everywhere** signs you out of every browser and device, this one included.
- **Personal access tokens**: **Manage tokens** opens [API tokens](/account/tokens). See [Your account](/help/account#api-tokens).
- **Diagnostics**: **Collect diagnostics** downloads a file about your recent activity that you can send to whoever helps you with a problem.

### Sessions {#sessions-tab}

**Manage sessions** opens the list of devices and browsers signed in to your account. See [Your account](/help/account#sessions).

### Appearance {#appearance-tab}

- **Theme**: **Light**, **Dark** or **System** (follows your computer or phone).
- **Language**: **English** or **العربية**.
- **Density**: **Comfortable** or **Compact** (tighter spacing, more on screen).

Changes apply at once and are kept in this browser.

### Privacy {#privacy-tab}

The **Activity tracking** switch and your recent history. See [Your account](/help/account#privacy).

### Models {#models-tab}

**Model Management** lists the AI models Maktaba uses, in three groups: **Speech-to-Text** (transcription), **Embedding** (meaning search) and **Diarization** (telling speakers apart). Each card shows the model's size, where it runs and its state.

Admins can:

- **Download** a model to this server. A bar shows the progress.
- **Activate** a downloaded model so Maktaba uses it.
- **Test** a model to check it works. You see **Test passed** with the time it took, or **Test failed**.
- **Delete** a downloaded model to free disk space.
- Pick the **Active STT backend**: the speech-to-text model used for transcription. Only downloaded models are offered.
- Enter an **OpenAI API key** and press **Save**, for the models that run at OpenAI.

Other users see the list without these buttons.

### Rules {#rules-tab}

Every setting for planning channels, tagging, finding metadata, Live TV, updates and more. See [Rules](/help/rules).

### Advanced {#advanced-tab}

**Experiments**: early features you can choose to try before everyone gets them.

### Storage & discovery {#storage-tab}

Admins only: where the server finds your video folders, whether devices on your network find it by themselves, its addresses, and a button to restart it. See [Storage & discovery](/help/storage).

### Subscription {#subscription-tab}

Your plan, the license and Maktaba Cloud. See [Subscription](/help/cloud#billing).

### About {#about-tab}

Shows the **Current version**, the **Update channel** and when Maktaba **Last checked** for updates. **Check now** looks for a new release right away. Admins also see **Automatic updates** here. See [Updates](/help/updates).

## What the states and messages mean {#states}

- **Active**: the model in use.
- **Downloaded**: on this server, not in use. Press **Activate** to use it.
- **Downloading**: being downloaded; the bar shows how far.
- **Available**: can be downloaded.
- **No models in this category.**: nothing is offered for this group.
- **Test passed (… ms)**: the model answered, in that many milliseconds.
- **Test failed**: the model did not answer correctly. See below.
- **Collecting…** / **Diagnostics bundle downloaded.**: the diagnostics file is being made, then saved to your downloads.
- **Update available** (a dot on **About**): a new release is ready. See [Updates](/help/updates).

## FAQ {#faq}

### Why can I not change the models? {#faq-models-admin}

Downloading, activating, testing and deleting models are for admins only.

### Which speech-to-text model should I use? {#faq-which-model}

Keep the one Maktaba set up unless you have a reason to change it. If you switch, **Test** the new one first.

### What is in the diagnostics file? {#faq-diagnostics}

Technical details about your own recent activity on this server, to help find a problem. Admins can collect a bundle for the whole system from [Logs](/help/logs).

### Why does my theme change back on another computer? {#faq-theme-per-browser}

Theme, language and density are kept per browser. Set them on each device.

## When something goes wrong {#troubleshooting}

### Could not load models. {#models-load-error}

- **What it means:** The page could not read the model list from the server.
- **Does Maktaba fix it itself?** No.
- **What you can do:** Reload the page. If it keeps failing, check [System health](/help/system-health).

### A model download stops or fails {#model-download-failed}

- **What it means:** The model could not be downloaded, often because of the network or a full disk.
- **Does Maktaba fix it itself?** No.
- **What you can do:** Check the server's free disk space and its internet access, then press **Download** again. The error message on the page names the reason.

### Test failed {#model-test-failed}

- **What it means:** The model did not give a correct answer when tried. For a model that runs at OpenAI, the API key may be missing or wrong.
- **Does Maktaba fix it itself?** No.
- **What you can do:** For an OpenAI model, enter the **OpenAI API key** again and press **Save**. For a local model, **Delete** it and **Download** it again. Keep the old model active until the new one passes.

### Could not collect diagnostics {#diagnostics-error}

- **What it means:** The diagnostics file could not be made or downloaded. The reason follows the message.
- **Does Maktaba fix it itself?** No.
- **What you can do:** Try again in a moment. If it keeps failing, ask an admin to collect it from [Logs](/admin/logs).
