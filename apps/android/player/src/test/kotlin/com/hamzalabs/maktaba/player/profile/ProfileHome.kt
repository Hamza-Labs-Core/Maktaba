package com.hamzalabs.maktaba.player.profile

import com.hamzalabs.maktaba.player.harness.FakeHome
import com.hamzalabs.maktaba.player.harness.FakeHome.Companion.json
import mockwebserver3.MockResponse

/**
 * The Profile's routes on the fake home, in the API's shapes (the client's ProfileApiTest
 * fixtures, which a Go test checks against the handlers, carry the same fields).
 */
object ProfileHome {
    const val HISTORY = """{"items":[
        {"video_id":"v2","title":"Omar, Episode 1","duration_sec":3000,"times_watched":1,"total_watch_sec":750,"best_percent":25,"last_watched_at":"2026-10-31T20:00:00Z","position_sec":750,"completed":false},
        {"video_id":"v1","title":"The Message","duration_sec":10680,"times_watched":3,"total_watch_sec":10680,"best_percent":100,"last_watched_at":"2026-10-29T18:00:00Z","position_sec":10680,"completed":true},
        {"video_id":"v4","title":"The Night Journey","duration_sec":2400,"times_watched":1,"total_watch_sec":600,"best_percent":25,"last_watched_at":"2026-10-20T09:00:00Z","position_sec":600,"completed":false}
    ],"limit":50,"offset":0}"""

    const val SESSIONS = """{"items":[
        {"id":"s1","created_at":"2026-10-27T09:00:00Z","last_seen_at":"2026-10-31T08:00:00Z","expires_at":"2026-11-26T09:00:00Z","ip":"192.168.1.20","user_agent":"Mozilla/5.0 (Macintosh; Intel Mac OS X 15_0) AppleWebKit/605.1.15 Version/18.0 Safari/605.1.15","current":false},
        {"id":"s2","created_at":"2026-10-15T12:00:00Z","last_seen_at":"2026-10-25T12:00:00Z","expires_at":"2026-11-14T12:00:00Z","ip":null,"user_agent":"Mozilla/5.0 (Windows NT 10.0) Chrome/130.0 Safari/537.36","current":false}
    ]}"""

    const val PLAYLISTS = """{"items":[
        {"id":"p1","name":"Ramadan","description":"The month's lessons","videos":2,"duration_sec":5400,"poster_video_ids":["v3","v5"],"created_at":"2026-10-01T09:00:00Z","updated_at":"2026-10-29T09:00:00Z","hand_ordered":true}
    ]}"""

    const val PLAYLIST = """{"playlist":{"id":"p1","name":"Ramadan","description":"The month's lessons","videos":2,"duration_sec":5400,"poster_video_ids":["v3","v5"],"created_at":"2026-10-01T09:00:00Z","updated_at":"2026-10-29T09:00:00Z","hand_ordered":true},
        "items":[
        {"id":"v3","title":"Lion of the Desert","filename":"v3.mkv","library_id":"l1","duration_sec":2700,"state":"ready","created_at":"2026-10-21T09:00:00Z","position_sec":900,"completed":false},
        {"id":"v5","title":"Andalusia, a History","filename":"v5.mkv","library_id":"l1","duration_sec":2700,"state":"ready","created_at":"2026-10-20T09:00:00Z"}
    ]}"""

    const val FAVORITES = """{"items":[
        {"id":"v1","library_id":"l1","filename":"v1.mkv","title":"The Message","duration_sec":10680},
        {"id":"v3","library_id":"l1","filename":"v3.mkv","title":"Lion of the Desert","duration_sec":2700}
    ],"next":null}"""

    const val VERSION = """{"version":"0.3.52","role":"home"}"""

    private val noContent: MockResponse get() = MockResponse.Builder().code(204).build()

    /** Answers every Profile route on [home]; a DELETE of a history row or a session answers 204. */
    fun install(home: FakeHome) {
        home.answer("/api/me/history") { json(HISTORY) }
        listOf("v1", "v2", "v4").forEach { id -> home.answer("/api/me/history/$id") { noContent } }
        home.answer("/api/me/sessions") { json(SESSIONS) }
        listOf("s1", "s2").forEach { id -> home.answer("/api/me/sessions/$id") { noContent } }
        home.answer("/api/me/playlists") { json(PLAYLISTS) }
        home.answer("/api/me/playlists/p1") { json(PLAYLIST) }
        home.answer("/api/me/videos") { json(FAVORITES) }
        home.answer("/api/system/version") { json(VERSION) }
    }
}
