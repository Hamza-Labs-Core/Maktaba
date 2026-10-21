# Library access

Choose which libraries each user can see. Only administrators see this page.

## What it is {#what-it-is}

**Administration → Library access** ([Library access control](/admin/library-acl)) is a grid: one row per user, one column per library. Each cell says what that user may do in that library.

- A user sees, searches and plays only the libraries where their cell is not **None**.
- Administrators always see every library, whatever their cells say.
- A new user starts with **None** everywhere, so they see nothing until you give them a library.

## How to use it {#how-to-use}

### Give a user a library {#give-a-library}

1. Open [Administration → Library access](/admin/library-acl).
2. Find the user's row and the library's column.
3. Pick **Read** in that cell.
4. Change as many cells as you like. The button at the top counts them: **Save 3 change(s)**.
5. Click it. The message _Access permissions saved._ confirms it.

### Take a library away {#remove-a-library}

Set the cell back to **None** and save. The user stops seeing that library.

Until you click **Save**, nothing changes for anyone. Leaving the page drops unsaved changes.

## What the states and messages mean {#states}

- **None**: the user does not see this library.
- **Read**: the user sees and plays this library.
- **Write** and **Admin**: today these give the same access as **Read**. Changing settings and content is still done by administrator accounts.
- **Save N change(s)**: N cells differ from what is saved. **Save** (greyed out) means nothing is changed.
- **Access permissions saved.**: the changes are stored.
- **Nothing to map**: there is no user or no library yet. Add a user on [Users](/help/users) and a library first.

## FAQ {#faq}

### When does the user see the change? {#faq-when}

Within about 15 minutes, when their app renews its sign-in, or at once if they sign out and in again.

### Do I need to give administrators libraries? {#faq-admins}

No. Administrators see every library. Their cells do not limit them.

### Does this control Live TV channels too? {#faq-live-tv}

No. Who may watch Live TV is set on [Live TV access](/admin/live-tv). See [Live TV administration](/help/live-tv-admin).

## When something goes wrong {#troubleshooting}

### A user still cannot see a library {#user-cannot-see}

- **What it means:** the change is saved but the user's sign-in still carries the old list.
- **Does Maktaba fix it itself?** Yes. The sign-in renews within about 15 minutes.
- **What you can do:** check that you clicked **Save**, then ask the user to sign out and sign in again.

### Something went wrong (with Try again) {#load-failed}

- **What it means:** the grid could not be loaded from the server.
- **Does Maktaba fix it itself?** No. The page waits for you.
- **What you can do:** click **Try again**. If it keeps failing, check [System health](/help/system-health).

### role must be one of none, read, write, admin {#bad-role}

- **What it means:** the server refused a cell value. This should not happen from this page.
- **Does Maktaba fix it itself?** No. Nothing in that save is stored.
- **What you can do:** reload the page and make the change again.
