# Users

Create accounts, make someone an administrator, reset a forgotten password and decide whether people can sign themselves up. Only administrators see this page.

## What it is {#what-it-is}

**Administration → Users** ([User management](/admin/users)) is where an administrator manages the people who sign in to Maktaba.

- Each account has a **Username**, a password and a **Role**: **Administrator** or **User**.
- Administrators see every library and every admin page. Users see only the libraries you give them on [Library access](/help/library-access).
- There is no email: a forgotten password is reset here by an administrator.

The table lists the users created or edited **in this browser**. The server does not hand out a full list of accounts, so a user made elsewhere (another browser, the Mac app's setup, self sign-up) is not in the table until you add or edit them here. The actions still work for any user.

## How to use it {#how-to-use}

### Add a user {#add-a-user}

1. Open [Administration → Users](/admin/users) and click **Add user**.
2. Type a **Username** and a **Password**.
3. Tick **Administrator** only for someone who should manage Maktaba.
4. Click **Save**. The message _User created._ confirms it.

Then give the new user their libraries on [Library access](/admin/library-acl). Administrators need nothing there; they see everything.

### Change a user {#edit-a-user}

Click **Edit** on the user's row. You can change the username, set a **New password** (optional) or tick or untick **Administrator**. **Save** stays greyed out until you change something.

### Reset a password {#reset-a-password}

1. Click **Reset password** on the user's row.
2. Choose **Make a temporary password** or **Type the new password** (see below).
3. Click **Reset password**.

With **Make a temporary password**, Maktaba makes one and shows it once. Click **Copy** and give it to the person. When they sign in with it, Maktaba asks them to choose their own.

With **Type the new password**, use at least 8 characters. Tick **Ask them to change it when they next sign in** if they should pick their own.

Either way the person is signed out everywhere, on the web and in the apps. The message says how many sign-ins ended.

### Let people sign themselves up {#open-registration}

Tick **Open registration: anyone who can reach this server can create an account** to let anyone who can open Maktaba create an account from the sign-in page (**Create an account**). Untick it to close sign-up; then only an administrator creates accounts, and the [Register page](/register) says registration is closed. It is off by default.

### Unlock an account {#unlock}

After 5 wrong passwords in a row, an account is locked for 15 minutes. Click **Unlock** to let the person try again at once.

### Sign someone out {#revoke-access}

**Revoke access** ends one sign-in by its **Session ID** (**Revoke session**) or one app's sign-in by its **Refresh-token family ID** (**Revoke token family**). To sign a person out everywhere, it is simpler to use **Reset password**.

### Delete a user {#delete-a-user}

Click **Delete**, then **Delete** again in the box. This cannot be undone.

## What the states and messages mean {#states}

- **Administrator**: can manage Maktaba and sees every library.
- **User**: sees only the libraries given on Library access.
- **No tracked users**: no user has been created or edited in this browser yet. It does not mean the server has no accounts.
- **User created.** / **User updated.** / **User deleted.**: the change is saved on the server.
- **Account unlocked.**: the person can sign in again now.
- **Password reset. N sign-ins ended.**: the new password is set and N open sign-ins were closed.
- **Registration is open.** / **Registration is closed.**: the sign-up setting was saved.
- **Choose a new password** (what the user sees): an administrator reset their password and asked for a new one. They type the **Temporary password**, a **New password** of at least 8 characters, and click **Save the new password**.

## FAQ {#faq}

### Why is a user I know about missing from the table? {#faq-missing-user}

The table only remembers users created or edited in this browser. Another administrator's browser has its own list. The account still exists and can sign in.

### Can a user reset their own password? {#faq-self-reset}

Not when they have forgotten it: there is no email, so an administrator resets it here. A signed-in user changes their own password with **Change password** on their [Account](/account) page.

### What if the temporary password is lost? {#faq-temp-lost}

The temporary password is shown only once. If it is lost, do another **Reset password**.

### Does open registration give new users any libraries? {#faq-registration-libraries}

No. A self-made account is a plain **User**. Give it libraries on [Library access](/help/library-access).

### Can I remove the last administrator? {#faq-last-admin}

No. Maktaba refuses to delete or demote the last administrator, so you can never lock yourself out.

## When something goes wrong {#troubleshooting}

### username already exists {#username-taken}

- **What it means:** another account already uses that username.
- **Does Maktaba fix it itself?** No. Nothing is saved.
- **What you can do:** pick another username. On the sign-up page the same problem shows as _That username or email is already taken._

### operation would leave the system with no admin {#last-admin}

- **What it means:** you tried to delete or demote the only administrator.
- **Does Maktaba fix it itself?** It refuses the change, so the account stays as it was.
- **What you can do:** make another user an **Administrator** first, then change this one.

### seat limit reached for the current subscription tier {#seat-limit}

- **What it means:** your plan allows a fixed number of accounts and they are all used.
- **Does Maktaba fix it itself?** No. The user is not created.
- **What you can do:** delete an account you no longer need, or see your plan on [Billing & license](/help/cloud#billing).

### A user cannot sign in after several tries {#locked-out}

- **What it means:** after 5 wrong passwords the account is locked for 15 minutes. The sign-in page may only say _Something went wrong._
- **Does Maktaba fix it itself?** Yes. The lock ends by itself after 15 minutes.
- **What you can do:** click **Unlock** on the user's row, or **Reset password** if they have forgotten it.

### Registration is closed on this server {#registration-closed}

- **What it means:** someone tried to sign up while open registration is off.
- **Does Maktaba fix it itself?** No.
- **What you can do:** create the account with **Add user**, or tick open registration if you want people to sign themselves up.

### The temporary password is not right {#wrong-temporary-password}

- **What it means:** on **Choose a new password**, the temporary password typed does not match.
- **Does Maktaba fix it itself?** No.
- **What you can do:** check the password you were given (copy it rather than retyping). If it is lost, ask an administrator for a new **Reset password**.
