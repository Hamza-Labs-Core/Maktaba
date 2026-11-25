### Changed

- **No source badge on a collection from a YouTube channel, and no Plex kind.** A YouTube
  channel's collection no longer carries a "YouTube channel" badge, and no app shows a "Plex
  collection" badge or a "From Plex" kind any more: collections are no longer read in from Plex,
  and a home's are from YouTube playlists and channels. The collections show as before, a YouTube
  playlist's with its badge. In every app: the iPhone, iPad, Mac and Apple TV apps, the Android
  and Android TV apps, the web app and the big screen. `GET /api/libraries/{id}/collections` no
  longer has a `plex` kind; an older app's `kind=plex` gets an empty list, not an error. A
  collection an older release read in from Plex lists as a curated one that an admin can edit.
