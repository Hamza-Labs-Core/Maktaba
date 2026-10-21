# Getting started

Sign in, find your way around Maktaba, and set it up the way you like.

## What it is {#what-it-is}

Maktaba is your library of lectures and shows. You open it in a web browser (or in the Maktaba app on a Mac), sign in with your own account, and from there you can browse, search, watch and, if you are an admin, run the whole system.

## How to use it {#how-to-use}

### Sign in {#sign-in}

1. Open the [sign-in page](/login).
2. Type your **Username** and **Password**.
3. Press **Sign in**. You land on the Library, or on the page you were trying to open.

If your home has several Maktaba machines, the sign-in page may offer to sign in through the home instead. Use it: one sign-in then works on every machine.

### Create an account {#register}

Press **Create an account** on the sign-in page, or open [Create your account](/register). Fill in **Username**, **Email**, **Password** and **Confirm password**, then press **Create account**.

Most servers keep registration closed. Then an admin makes your account for you (see [Users](/help/users)).

### Choose a new password when asked {#forced-password-change}

If an admin reset your password, the first thing you see after signing in is **Choose a new password**.

1. Type the **Temporary password** the admin gave you.
2. Type a **New password** of at least 8 characters, and type it again in **Confirm password**.
3. Press **Save the new password**.

You cannot use the app until you do this. **Sign out** leaves without changing it.

### Find your way {#find-your-way}

- On a wide screen, the menu on the side lists **Library**, **Series**, **TV Guide**, **Search**, **Processing**, **Transcription** and **Settings**, then your account pages. Admins also see **Administration**.
- On a phone, the bar at the bottom has **Library**, **Series**, **TV Guide**, **Search**, **Processing** and **More**. [More](/more) holds everything else: **Settings**, **Transcription**, your account pages, the admin pages and **Sign out**.
- The search box at the top searches your whole library. Press Enter to open the results.

### Language and theme {#language-and-theme}

- The **AR** / **EN** button at the top switches between Arabic and English. The whole app flips to right-to-left in Arabic.
- The sun / moon button cycles the theme: light, dark, then the same as your system.
- For all three choices in one place, including **Density** (**Comfortable** or **Compact**), open [Settings → Appearance](/settings/appearance).

These choices are kept in this browser.

### Keyboard shortcuts {#keyboard-shortcuts}

Press `?` anywhere (outside a text box) to see the list. Press `Esc` to close it.

- `g` then `l`: go to Library
- `g` then `s`: go to Search
- `g` then `q`: go to Processing
- `g` then `c`: go to Settings
- `/`: jump to the search box

Press the second key of a `g` pair within about a second. The player has its own keys; see [Videos and the player](/help/videos#player-keys).

### Notifications {#notifications}

The bell at the top shows how many notices you have not read. Open it to see them, newest first. Click one to go to what it is about; it is then marked read. **Mark all read** clears the count. The bell checks for new notices about every 30 seconds.

### When the app has a new version {#app-update-prompt}

After Maktaba is updated, the page you have open may show **A new version is available.** Press **Reload** to load it. Nothing you saved is lost.

### First run on a Mac {#first-run}

On a Mac, Maktaba.app walks you through setup the first time: what will run, your admin account, your library folders, the transcription models, Plex (optional), then the install. See [The Mac app](/help/mac-app) for each step.

## What the states and messages mean {#states}

- **Invalid username or password.**: the username or password is wrong. Check Caps Lock and the keyboard language.
- **Registration is closed on this server. Ask an administrator to create an account for you.**: this server does not let people sign up by themselves.
- **That username or email is already taken.**: pick another username, or sign in if it is yours.
- **Passwords do not match.**: the two password boxes are different.
- **Use at least 8 characters.**: the new password is too short.
- **Choose a password that is not the temporary one.**: the new password must differ from the temporary one.
- **The temporary password is not right.**: type the temporary password exactly as the admin gave it.
- **Nothing new.**: you have no notifications.
- **Could not load the notifications.**: the bell could not reach the server. It tries again by itself.
- **Update available** (a dot on Settings or More): a new Maktaba release is ready. See [Updates](/help/updates).

## FAQ {#faq}

### I forgot my password. What do I do? {#faq-forgot-password}

Ask an admin to reset it. They give you a temporary password, and you choose your own the next time you sign in.

### Why is there no Administration section in my menu? {#faq-no-admin}

Only admins see it. Ask an admin if you need access to those pages.

### Does my language choice follow me to other devices? {#faq-language-devices}

No. Language, theme and density are kept in each browser. Set them once on each device.

### Do the shortcuts work with an Arabic keyboard? {#faq-shortcuts-arabic}

The page shortcuts use the letter you type, so switch the keyboard to English for them. The player shortcuts work with either layout.

## When something goes wrong {#troubleshooting}

### Invalid username or password. {#wrong-password}

- **What it means:** Maktaba did not accept the username and password together.
- **Does Maktaba fix it itself?** No. After 5 wrong tries in a row the account is locked for 15 minutes, and then you can try again.
- **What you can do:** Check Caps Lock and the keyboard language and try again. If you are not sure of the password, ask an admin to reset it.

### Something went wrong. (when signing in) {#sign-in-error}

- **What it means:** The server refused the sign-in for a reason other than a wrong password. Most often the account is locked after too many wrong tries, or the server cannot be reached.
- **Does Maktaba fix it itself?** A lock ends by itself after 15 minutes.
- **What you can do:** Wait 15 minutes and try again, or ask an admin to press **Unlock** on your account in [Users](/admin/users).

### Registration is closed {#registration-closed}

- **What it means:** This server only lets admins create accounts.
- **Does Maktaba fix it itself?** No.
- **What you can do:** Ask an admin to create your account, then sign in.

### The page keeps asking me to choose a new password {#must-change-loop}

- **What it means:** An admin reset your password and the new one has not been saved yet.
- **Does Maktaba fix it itself?** No.
- **What you can do:** Fill in all three boxes and press **Save the new password**. If **The temporary password is not right.** keeps showing, ask the admin for a new temporary password.
