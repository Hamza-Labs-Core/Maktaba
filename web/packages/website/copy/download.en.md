# The download page: copy (English)

Story 60.02 part B. Each `##` heading is a key; the text under it is the string, in the
voice of [voice.md](voice.md); the Arabic is in [download.ar.md](download.ar.md), with
the same keys. `{version}`, `{date}`, `{size}` and `{file}` are filled from the release
at build time (60.11); the mockups fill them from v0.3.131. The requirements lines come
from `downloads.json` in 60.11, and are written here first.

## meta.title

Download Maktaba

## meta.description

Download Maktaba for Mac, Windows, Linux or your NAS. The computer that holds your films runs it, and every other screen watches.

## social.title

Download Maktaba for your home

## h1

Download Maktaba

## lead

Start with the computer or NAS that holds your films: it runs Maktaba for the whole house.

## version

Version {version}, released on {date}

## note

This looks like a Mac. If it isn’t, every system is below.

## mine

For this computer

## version.label

Version

## date.label

Released

## size.label

Size

## other

Other formats

## verify

Verify this download

## mac.name

Mac

## mac.needs

A Mac with Apple silicon, macOS 13 or later

## mac.primary

Download for Mac

## mac.other.core

Maktaba Core, the lighter app

## mac.verify.text

GitHub records this SHA-256 for the file. To compare, run this in Terminal where the file is:

## mac.verify.after

The app checks the signature of every update itself before it installs one.

## windows.name

Windows

## windows.needs

Windows 10 or later, 64-bit

## windows.primary

Download for Windows

## windows.other.portable

Portable, in a folder of its own (`.zip`)

## windows.other.player

Maktaba Player alone, for a computer that only watches (`.msi`)

## linux.name

Linux

## linux.needs

A 64-bit PC with a desktop; ARM64 under Other formats

## linux.primary

Download the AppImage

## linux.other.deb

For Debian and Ubuntu (`.deb`)

## linux.other.rpm

For Fedora and openSUSE (`.rpm`)

## linux.other.arm

For ARM64 (AppImage)

## linux.other.player

Maktaba Player alone (`.deb`)

## linux.other.headless

For a machine without a desktop: `install.sh`

## nas.name

NAS and Docker

## nas.needs

Synology, TrueNAS, Unraid, QNAP, UGREEN, or any machine with Docker

## nas.primary

Choose your NAS guide

## nas.other.compose

The compose file

## nas.other.synology

For Synology Container Manager

## nas.other.truenas

For TrueNAS, as a custom app

## nas.verify.text

This file is listed in the release’s checksums, which are signed for this release alone. Check the signature, then the file:

## guide

How to install

## need.h2

What do I need?

## need.p1

One computer or NAS in your home holds your films and runs Maktaba. Leave it on, and the whole house can watch.

## need.p2

Every other screen watches from it: in Maktaba Player on Windows and Linux, or in any browser at home, the TV’s included.

## need.home

The computer that holds your films

## need.tv

The TV

## need.phone

Phones and tablets

## need.computer

Other computers

## need.link

Find your install guide

## all

Every file in this release, on GitHub

## note.windows

This looks like a Windows PC. If it isn’t, every system is below.

## note.linux

This looks like a Linux computer. If it isn’t, every system is below.

## windows.verify.text

GitHub records this SHA-256 for the file. To compare, run this in PowerShell where the file is:

## linux.verify.text

GitHub records this SHA-256 for the file. To compare, run this in a terminal where the file is:

## verify.sig

The release also attaches {file}, the file’s signature: the app checks a signature like it before it installs any update.

## server.name

Server packages

## server.apt

Debian and Ubuntu, from the apt repository

## server.dnf

Fedora and RHEL, from the dnf repository

## server.brew

macOS, with Homebrew

## server.other.install

The install script (`install.sh`)

## server.other.linux-amd64

The server alone, Linux x86-64 (`.tar.gz`)

## server.other.linux-arm64

The server alone, Linux ARM64 (`.tar.gz`)

## server.other.darwin-arm64

The server alone, macOS with Apple silicon (`.tar.gz`)

## server.other.windows

The server alone, Windows (`.zip`)
