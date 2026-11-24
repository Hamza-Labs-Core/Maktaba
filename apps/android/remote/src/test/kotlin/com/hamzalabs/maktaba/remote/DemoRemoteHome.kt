package com.hamzalabs.maktaba.remote

import com.hamzalabs.maktaba.core.DemoLibrary
import com.hamzalabs.maktaba.core.api.User
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive
import java.time.Instant
import java.time.ZoneOffset

/**
 * The Remote's answers for the demo library's home (stories 60.09 and 60.14): the fixture
 * (`tests/fixtures/demo-library`) has a household, its films and its clock but none of a home
 * admin's screens, so they are made up here, on the fixture's own titles and clock and in its
 * language. No person is named in them: the signed-in account is the fixture's (`haddad`), and the
 * machines are named for where they stand in the house.
 *
 * - Needs attention: three causes, one that Retry cannot fix (the disk is full: its settings);
 * - the queue: four stages and the jobs running now on three of the library's films;
 * - updates: the version the home runs, the next one waiting for the update window, the last
 *   update done, and two machines up to date;
 * - the Overview dashboard: the home's last 24 hours (requests, load and free memory);
 * - a TV in the living room asking to sign in with a code.
 */
object DemoRemoteHome {
    /** The code the living room's TV shows. */
    const val CODE = "KQ7M-4TXP"

    /** The update run the screens show (the last one). */
    const val RUN = "r41"

    /** The stage the tablets open beside the queue. */
    const val STAGE = "transcribe"

    private val json = Json { ignoreUnknownKeys = true }

    /** The demo library's account, as the Remote signs it in. */
    fun user(): User = DemoLibrary.load("en").let { User(id = it.userId, username = it.username, isAdmin = it.isAdmin) }

    /** A film's title in [demo]'s language. */
    fun title(demo: DemoLibrary, id: String): String =
        json.parseToJsonElement(demo.text("get-videos-$id.json")).jsonObject["title"]!!.jsonPrimitive.content

    /** [s] as a JSON string's body (quotes and backslashes escaped). */
    private fun q(s: String) = s.replace("\\", "\\\\").replace("\"", "\\\"")

    /** Every answer the demo shots read, on [home], for [demo]. */
    fun install(home: FakeRemoteHome, demo: DemoLibrary) {
        val now = demo.clock
        val ar = demo.lang == "ar"
        fun at(secondsAgo: Long) = now.minusSeconds(secondsAgo).toString()

        home.answer("/api/pairing/pending") { FakeRemoteHome.json("""{"count":0,"requests":[]}""") }
        home.answer("/api/issues") { FakeRemoteHome.json(ISSUES) }
        listOf("probe", "transcribe", "subtitle_gen", "chapter_thumbs").forEach { s ->
            home.answer("/api/queues/$s/failures") { FakeRemoteHome.json("""{"groups":[]}""") }
        }
        home.answer("/api/queues") { FakeRemoteHome.json(QUEUES) }
        val running = listOf(
            Triple("v-lake-nasser", "transcribe", """{"step":"","steps":[],"done_units":72,"total_units":120}"""),
            Triple("v-ghost-octopus", "subtitle_gen", """{"step":"","steps":[],"done_units":3,"total_units":8}"""),
            Triple("v-llamigos", "probe", "null"),
        )
        home.answer("/api/queues/running") {
            FakeRemoteHome.json(
                running.mapIndexed { i, (id, stage, progress) ->
                    """{"id":${71 + i},"stage":"$stage","state":"${if (progress == "null") "claimed" else "running"}","subject_kind":"video",""" +
                        """"subject_id":"$id","subject_label":"${q(title(demo, id))}","attempts":1,"max_attempts":3,""" +
                        """"started_at":"${at(600L * (i + 1))}","progress":$progress}"""
                }.joinToString(",", """{"host":"","jobs":[""", "]}"),
            )
        }

        val main = if (ar) "الحاسوب الرئيسي" else "Main computer"
        val upstairs = if (ar) "حاسوب الطابق العلوي" else "Upstairs computer"
        // the last update ran in the night's window, 03:00 UTC yesterday
        val started = now.atOffset(ZoneOffset.UTC).minusDays(1).withHour(3).withMinute(0).toInstant()
        fun after(minutes: Long): String = started.plusSeconds(minutes * 60).toString()
        home.answer("/api/cluster/updates") {
            FakeRemoteHome.json(
                """{"current_version":"0.3.90","watermark":"0.3.90",
                  "policy":{"auto_install":true,"channel":"stable","window":"03:00-05:00"},
                  "plan":{"target":{"version":"0.3.91","verify_state":"verified"},"newest":{"version":"0.3.91","verify_state":"verified"},
                          "held":false,"auto_install":true,"due":false,"waiting":"outside_window"},
                  "last_run":{"id":"$RUN","version":"0.3.90","from_version":"0.3.89","trigger":"auto","state":"done","step":"commit",
                              "started_at":"${after(0)}","finished_at":"${after(14)}"},
                  "catalog":[{"version":"0.3.90","verify_state":"verified"},{"version":"0.3.91","verify_state":"verified"}],
                  "last_check_ok":"${at(3600)}","check_stale":false,
                  "machines":[
                    {"id":"home","name":"$main","role":"home","version":"0.3.90","state":"up_to_date","platform":"linux/amd64",
                     "last_update":{"at":"${after(0)}","ended_at":"${after(9)}","source":"run","from":"0.3.89","to":"0.3.90","outcome":"installed"}},
                    {"id":"n1","name":"$upstairs","role":"node","version":"0.3.90","state":"up_to_date","platform":"linux/amd64",
                     "last_update":{"at":"${after(9)}","ended_at":"${after(14)}","source":"run","from":"0.3.89","to":"0.3.90","outcome":"installed"}}]}""",
            )
        }
        home.answer("/api/cluster/updates/runs/$RUN") {
            val steps = listOf("preflight", "download", "verify", "backup", "switch", "watch", "commit")
            FakeRemoteHome.json(
                """{"id":"$RUN","version":"0.3.90","from_version":"0.3.89","trigger":"auto","state":"done","step":"commit",
                  "started_at":"${after(0)}","finished_at":"${after(14)}","install_kind":"appbundle","detail":{},
                  "steps":[${steps.mapIndexed { i, s -> """{"seq":${i + 1},"host":"home","step":"$s","state":"done","started_at":"${after(i.toLong())}"}""" }.joinToString(",")}],
                  "targets":[
                    {"node_id":"home","name":"$main","ord":0,"platform":"linux/amd64","state":"healthy","from_version":"0.3.89","to_version":"0.3.90","attempts":1,
                     "applied_at":"${after(6)}","healthy_at":"${after(9)}"},
                    {"node_id":"n1","name":"$upstairs","ord":1,"platform":"linux/amd64","state":"healthy","from_version":"0.3.89","to_version":"0.3.90","attempts":1,
                     "applied_at":"${after(12)}","healthy_at":"${after(14)}"}]}""",
            )
        }

        home.answer("/api/admin/dashboards/overview") { FakeRemoteHome.json(overview(now)) }

        // the living room's TV, asking to sign in with the code it shows
        val tv = if (ar) "تلفاز غرفة الجلوس" else "Living room TV"
        home.answer("/api/pairing/device/${CODE.replace("-", "")}") {
            // its code runs out in ten minutes (the card counts the minutes from this device's clock)
            val expires = Instant.ofEpochMilli(System.currentTimeMillis()).plusSeconds(570)
            FakeRemoteHome.json(
                """{"code":"$CODE","device_label":"${q(tv)}","expires_at":"$expires"}""",
            )
        }
    }

    /**
     * Needs attention: what a home shows after a night's work. The causes' titles are the app's
     * own words; the stages and the messages are the home's.
     */
    private val ISSUES = """{"groups":[
        {"stage":"fingerprint","cause":"nas_timeout","label":"NAS read timed out","count":3,"action":"retry","sample_message":"read timed out after 30 s"},
        {"stage":"check_clips","cause":"model_unavailable","label":"The model did not answer","count":2,"action":"retry","sample_message":"4 of 4 clips got no answer from the model"},
        {"stage":"chapter_thumbs","cause":"disk_full","label":"The disk is full","count":1,"action":"open_settings","sample_message":"no space left on device"}
    ]}"""

    /** The processing stages: their counts today, nothing paused. */
    private val QUEUES = """{"paused_all":false,"issues_count":6,
        "stages":[
          {"stage":"probe","label":"Probe","lane":"a","enabled":true,"paused":false},
          {"stage":"transcribe","label":"Transcribe","lane":"a","enabled":true,"paused":false},
          {"stage":"subtitle_gen","label":"Subtitles","lane":"a","enabled":true,"paused":false},
          {"stage":"chapter_thumbs","label":"Chapter pictures","lane":"b","enabled":true,"paused":false}],
        "queues":[
          {"stage":"probe","counts":{"pending":0,"ready":2,"running":1,"failed_open":0,"done_24h":14},"rate_per_min":1.5,"no_worker":false},
          {"stage":"transcribe","counts":{"pending":0,"ready":3,"running":1,"failed_open":0,"done_24h":6},"rate_per_min":0.2,"eta_sec":1500,"oldest_waiting_sec":2400,"no_worker":false},
          {"stage":"subtitle_gen","counts":{"pending":0,"ready":2,"running":1,"failed_open":0,"done_24h":9},"rate_per_min":0.4,"no_worker":false},
          {"stage":"chapter_thumbs","counts":{"pending":0,"ready":0,"running":0,"failed_open":1,"done_24h":31},"rate_per_min":0,"no_worker":false}]}"""

    /**
     * The Overview's last 24 hours to [now], an hour apart: the API's requests (busiest in the
     * evening, when the household watches), the load and the free memory.
     */
    private fun overview(now: Instant): String {
        val hours = (0 until 24).map { now.epochSecond - (23 - it) * 3600L }
        fun hourOf(t: Long) = Instant.ofEpochSecond(t).atOffset(ZoneOffset.UTC).hour
        // the evening's watching, 17:00 to 22:00 UTC, and a little in the morning
        fun watching(h: Int) = when (h) {
            in 17..21 -> 1.0 - kotlin.math.abs(h - 19) * 0.22
            in 7..9 -> 0.25
            else -> 0.05
        }
        fun series(name: String, labels: String, kind: String, value: (Int, Int) -> Double) =
            """{"name":"$name","labels":"$labels","kind":"$kind","points":[${hours.mapIndexed { i, t -> "[$t,${value(i, hourOf(t))}]" }.joinToString(",")}]}"""
        val all = listOf(
            series("http_request_duration_seconds_count", "", "counter") { i, h -> (3 + 38 * watching(h) + (i * 7) % 4).toInt().toDouble() },
            series("maktaba_host_load1", "machine=home", "gauge") { i, h -> "%.2f".format(java.util.Locale.ROOT, 0.4 + 2.2 * watching(h) + (i % 5) * 0.08).toDouble() },
            series("maktaba_host_memory_free_bytes", "machine=home", "gauge") { i, h -> 9_400_000_000.0 - 3_800_000_000.0 * watching(h) - (i % 3) * 150_000_000.0 },
        )
        return """{"board":"overview","range":"24h","series":[${all.joinToString(",")}]}"""
    }
}
