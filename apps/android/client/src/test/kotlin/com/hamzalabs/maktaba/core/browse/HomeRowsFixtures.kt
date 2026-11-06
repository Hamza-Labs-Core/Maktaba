package com.hamzalabs.maktaba.core.browse

/**
 * Home's answers for the row builder's tests (the shapes of the recorded
 * `client/src/test/resources/browse/home.json`, `recommendations.json` and `live/now.json`, 48.8),
 * with every row filled except Favorites. The Android Player's `HomeFixtures` has the same
 * answers for its screen tests. Scratch data: no real home or person.
 */
object HomeRowsFixtures {
    const val FEATURED_SERIES = "s-seerah"
    const val CONTINUE = "v-cw"
    const val ON_DECK = "v-deck"
    const val ADDED = "v-talk"
    const val WATCHED = "v-old"
    const val PINNED_LIB = "l-lectures"
    const val PINNED_VIDEO = "v-pin"
    const val REC_1 = "v-rec1"
    const val REC_2 = "v-rec2"
    const val REC_LIB = "v-rec3"
    const val CHANNEL = "c-quran"

    /** FR-10's rows the fixture fills, in their order (Favorites is empty: absent). */
    val ROW_ORDER = listOf(
        KEY_CONTINUE, KEY_ON_DECK, KEY_LIVE, KEY_ADDED, KEY_WATCHED, KEY_PINNED + PINNED_LIB, KEY_FOR_YOU, KEY_FROM_LIBRARY,
    )

    private fun card(id: String, title: String, extra: String = "") =
        """{"id":"$id","title":"$title","filename":"$id.mp4","library_id":"l1","duration_sec":2400,"state":"ready"$extra}"""

    val HOME = """
    {
      "featured": {"kind":"series","video":${card("v-s1", "سيرة ابن هشام ١")},
                   "series":{"id":"$FEATURED_SERIES","name":"سيرة ابن هشام","episodes":24},"picked":false},
      "continue_watching": [${card(CONTINUE, "Fiqh of Fasting 7", ""","position_sec":600,"completed":false""")}],
      "on_deck": [${card(ON_DECK, "Seerah 3", ""","series":{"id":"$FEATURED_SERIES","name":"سيرة ابن هشام"}""")}],
      "favorites": [],
      "recently_added": [${card(ADDED, "A talk")}],
      "recently_added_grouped": [
        {"kind":"series","video":${card("v-s1", "سيرة ابن هشام ١")},"series":{"id":"$FEATURED_SERIES","name":"سيرة ابن هشام"},"count":3},
        {"kind":"video","video":${card(ADDED, "A talk")}}
      ],
      "recently_watched": [${card(WATCHED, "Tafsir al-Baqarah", ""","position_sec":2400,"completed":true""")}],
      "pinned": [{"library":{"id":"$PINNED_LIB","name":"Lectures"},
                  "recently_added":[${card(PINNED_VIDEO, "Usul 1")}],
                  "continue_watching":[${card(CONTINUE, "Fiqh of Fasting 7", ""","position_sec":600""")}]}]
    }
    """.trimIndent()

    val RECS = """
    {"rails":[
      {"id":"continue","title":"Continue Watching","reason_kind":"continue_watching","items":[{"video_id":"$CONTINUE","position_sec":600,"duration_sec":2400}]},
      {"id":"for-you","title":"For You","reason_kind":"for_you","items":[{"video_id":"$REC_1","duration_sec":900},{"video_id":"$REC_2","duration_sec":900}]},
      {"id":"library","title":"From your library","reason_kind":"from_library","items":[{"video_id":"$REC_LIB","duration_sec":900}]}
    ],"generated_at":"2026-10-31T08:00:00Z","expires_at":"2026-10-31T09:00:00Z","cache_hit":false}
    """.trimIndent()

    val NOW = """
    {"server_time":"2026-10-31T18:20:00Z","channels":[
      {"channel":{"id":"$CHANNEL","number":1,"name":"قناة القرآن","slug":"quran","logo_url":"/api/channels/$CHANNEL/logo"},
       "current":{"channel_id":"$CHANNEL","kind":"program","start":"2026-10-31T18:00:00Z","stop":"2026-10-31T18:40:00Z","title":"تفسير سورة البقرة","poster":"/api/videos/v-183/poster","is_live":true,"progress":0.5},
       "next":null,"on_air":true}
    ]}
    """.trimIndent()

    val EMPTY_HOME = """{"featured":null,"continue_watching":[],"on_deck":[],"favorites":[],"recently_added":[],"recently_added_grouped":[],"recently_watched":[],"pinned":[]}"""
    const val EMPTY_RECS = """{"rails":[]}"""

}
