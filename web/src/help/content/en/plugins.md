# Plugins

See every plugin on your home, turn each one on or off, and find its problems and what it uses. Only administrators see this tab.

## What it is {#what-it-is}

**Settings → Plugins** ([open it](/settings/plugins)) lists every plugin on your home in one list. A plugin adds jobs to the queue: making thumbnails, writing down what is said, building Live TV, and whatever else a plugin was written to do.

Each plugin is marked with who publishes it:

- **By Maktaba, verified**: Maktaba publishes it. Today these plugins come with Maktaba and are updated when Maktaba is updated.
- **From the store**: someone else published it and the plugin store checked it.
- **From a file**: an admin installed it from a file.

Some parts of Maktaba must always run, such as the library scan. They are part of Maktaba itself, not plugins: they have no card and no switch. A line under the list names them and links to [Components](/settings/components) and [System health](/admin/health).

## How to use it {#how-to-use}

### Find a plugin {#filter}

The buttons above the list show **All** plugins, the ones **By Maktaba**, the ones **By others**, or the ones installed **From a file**. Each button says how many it shows.

### Read a card {#card}

A card has the plugin's name and version, who publishes it, what it is doing now in words, its switch, how many jobs it finished and how many failed in the last day, its open problems, and the memory of the program that runs it with how many jobs are running and waiting.

### Turn a plugin on or off {#switch}

Use the switch on the plugin's card or at the top of its page. Off keeps the plugin's settings and files; it only stops it running, and its queued jobs wait.

When turning a plugin off touches anything else, a dialog names it first:

- other plugins or parts that need it and will wait,
- parts that keep running with less,
- what people using your home lose (for example **Subtitles**),
- the programs and background tasks that stop with it,
- how many queued jobs will wait.

Press **Cancel** and nothing changes.

A plugin that cannot be turned on has its switch greyed out while it is off, and the reason is written beside it, for example **This home's plan does not include this plugin** or **Stopped. Press Start to let it run again.** A plugin that is on can always be turned off.

If Maktaba cannot read what else turning a plugin off would stop, it turns nothing off: the dialog says so, and you can **Try again** or **Turn off anyway**.

When you turn on a plugin that cannot run beside something that is on, a dialog names what it would turn off. Press **Turn on** to switch, or **Cancel**.

### Install a plugin {#install}

1. Press **Browse the store**, open a plugin and press **Install**. The store checks every plugin before it lists it.
2. Or press **Install from a file** and choose a `.mkplugin` file you were given.
3. On the plugin's page, tick what it may do and which libraries it may see. It sees no library until you tick one.
4. Turn it on with its switch.

**Unreviewed** means nobody at the store checked the file. It still runs in a sandbox with no internet and sees only what you allow, but install it only if you trust who gave it to you. Maktaba asks you to type the plugin's name first, and **Allow unreviewed plugins** must be on.

Plugins by Maktaba cannot be installed or removed on their own yet. Turn off the ones you do not want.

### Open a plugin's page {#page}

Press a plugin's name or **Open**. The page has its own address, so you can bookmark it or send it to another admin. It shows:

- **Overview**: what it does, its version, who publishes it and where it came from.
- **What it does, step by step**: each step with a link to its queue, how long a job usually takes and where the step runs.
- **Permissions**: what it may do, in Maktaba's words.
- **Status**: what it is doing on each machine.
- **What it uses**: see [below](#resources).
- **Problems**: see [below](#errors).
- **Settings**: its own settings, or links to where its settings are in Rules and Models.
- **Versions**: update or roll back a plugin from the store or a file. A plugin by Maktaba is updated with Maktaba, with a link to Updates.
- **What it wrote**: for a plugin from the store or a file, with **Revert**.
- **Turn off or remove**: Stop and Start, and Remove where that applies.

A section that does not apply to a plugin is not shown.

### See what a plugin uses {#resources}

**What it uses** shows the jobs running and waiting, how long a job usually takes, and the memory of each program that runs the plugin, on each machine.

Several plugins often run inside one program. The page says so: it shows the whole program's memory and names the other plugins it runs. Maktaba does not split that number between plugins, because it cannot measure each plugin's part.

A plugin from the store or a file also shows how much of its own storage it used, with a bar, and the memory its jobs used lately against what one job may use.

Processor use is not measured for each program yet.

### Find a plugin's problems {#errors}

The problems count on a card opens the failures list showing only that plugin's steps. On the plugin's page, **Problems** has the same link, **Its errors in the logs** (the Logs page showing only this plugin's errors) and **Its logs**.

## States {#states}

A plugin's card says what it is doing in the same words as [Components](/help/components#states): **Running**, **Off**, **Waiting**, **Stopped**, **Finishing work** and so on, with the reason.

## Questions {#faq}

### Why can I not remove a plugin by Maktaba? {#faq-remove}

It comes with the Maktaba release your home runs. Turn it off to stop it running. Installing and removing these plugins one by one is planned.

### Is the switch here the same as on Components? {#faq-components}

Yes. A plugin by Maktaba does the work of one part on the Components page, and both pages show the same switch. Change it in either place.

### Why do two plugins show the same memory? {#faq-shared}

They run inside the same program. The number is the whole program's.

## Troubleshooting {#troubleshooting}

### The switch is greyed out {#locked}

A greyed switch means the plugin is off and cannot be turned on right now. Read the reason beside it. A stopped plugin has a **Start** button. A plugin your plan does not include needs a different plan. A plugin the store withdrew stays off.

### The memory says "Not measured yet" {#not-measured}

The program has not been measured since it started, or no program runs the plugin right now. Memory is read every few minutes.
