# Epic 56 research: how Plex's TV apps present a library

Read on 2026-11-05 from Plex's support articles. Plex is the reference because people moving
from it to Maktaba expect its layout; Epic 48's [research.md](../48-native-players/research.md)
covers Plex's apps more widely (navigation, player, downloads).

## Library tabs

- The big-screen apps show a library with a sidebar or top row of **Recommended, Library,
  Collections, Playlists and Categories**. New installs open on Recommended.
  ([Navigating the Big Screen Apps](https://support.plex.tv/articles/navigating-the-big-screen-apps/))
- **Recommended** holds rows Plex builds (On Deck, Recently Added, Recently Released, by genre)
  plus any collection the admin chose to show there; the admin can reorder and hide rows.
  ([Manage Recommendations](https://support.plex.tv/articles/manage-recommendations/))
- **Library** is the grid, with a type picker (for a TV library: shows, seasons, episodes), sort,
  filters, and a jump bar of letters.
  ([Using the Library View](https://support.plex.tv/articles/200392126-using-the-library-view/),
  [Library](https://support.plex.tv/articles/200289526-library/))
- **Playlists** lists the user's playlists that have items from this library.
- **Categories** groups by metadata: genre, year, decade, content rating, collection.

## Collections

- Collections are made by the server's admin, manual or smart (a saved filter and sort);
  deleting one never deletes its items; only the admin can delete one.
  ([Collections](https://support.plex.tv/articles/201273953-collections/))
- Plex makes some automatically (from TMDB collections, with a minimum size the admin sets).
  ([Advanced settings, Plex Movie agent](https://support.plex.tv/articles/advanced-settings-plex-movie-agent/))
- **Collection mode**, per library: disabled (collections only in their tab), hide the items that
  are in a collection (the collection's card stands in for them), or show both.
  ([Collections](https://support.plex.tv/articles/201273953-collections/),
  [Advanced settings, Plex TV Series agent](https://support.plex.tv/articles/advanced-setting-plex-tv-series-agent/))
- A collection can be promoted to a row on the library's Recommended tab and on Home.
  ([Manage Recommendations](https://support.plex.tv/articles/manage-recommendations/))

## Play, Shuffle and play queues

- Play on a library or a filtered view plays it in order; Shuffle plays it in a random order.
  Either one creates a **play queue**, which is temporary, not a playlist; Play next and Add to
  queue change it. ([Play Queues](https://support.plex.tv/articles/202188298-play-queues/))
- Play on a show starts at the On Deck episode; Shuffle shuffles every episode of the show or
  season.
- Smart shuffle (a server setting) weights music toward rated, popular, less recently played
  tracks; with it off, shuffle is plain random. Music only.

## Playlists

- Playlists belong to one user; they can be manual or smart (built from a filter, with a sort
  and a limit, and updating themselves). An admin can share a playlist with managed users.
- Plex's newer **Lists** (Watchlist and friends) are a separate, cloud-side feature and do not
  change playlists or collections. ([Lists](https://support.plex.tv/articles/lists/))

## What Maktaba takes, and what it leaves

Taken: the five tabs and their names, admin-only collections (manual, smart, automatic),
collection mode, promoted rows, per-user manual and smart playlists, server play queues with
Play next and Add to queue, show Play from the next unwatched episode, auto-play with a
countdown.

Left: smart shuffle, shared playlists (a later epic), Lists and Watchlist, metadata categories
that Maktaba does not have (cast, content rating); Maktaba's Categories are speakers, topics,
languages and years. Maktaba adds what Plex lacks: Arabic with a mirrored layout and D-pad, and
automatic collections from the YouTube playlists and channels its videos came from.
