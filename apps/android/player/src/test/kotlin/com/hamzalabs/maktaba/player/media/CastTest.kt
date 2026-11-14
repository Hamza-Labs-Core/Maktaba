@file:OptIn(UnstableApi::class)

package com.hamzalabs.maktaba.player.media

import android.content.Context
import androidx.annotation.OptIn
import androidx.media3.cast.CastPlayer
import androidx.media3.common.MediaItem
import androidx.media3.common.MediaMetadata
import androidx.media3.common.MimeTypes
import androidx.media3.common.Player
import androidx.media3.common.util.UnstableApi
import androidx.media3.exoplayer.ExoPlayer
import androidx.media3.test.utils.robolectric.ShadowMediaCodecConfig
import androidx.test.core.app.ApplicationProvider
import androidx.test.ext.junit.runners.AndroidJUnit4
import com.google.android.gms.cast.CastMediaControlIntent
import com.google.android.gms.cast.MediaInfo
import com.google.android.gms.cast.MediaMetadata as CastMetadata
import com.hamzalabs.maktaba.core.api.Video
import com.hamzalabs.maktaba.core.session.HomeInfo
import com.hamzalabs.maktaba.player.Playback
import com.hamzalabs.maktaba.player.R
import com.hamzalabs.maktaba.player.harness.FakeHome
import com.hamzalabs.maktaba.player.harness.NoStall
import com.hamzalabs.maktaba.player.watch.FakeStreams
import com.hamzalabs.maktaba.player.watch.idleFor
import com.hamzalabs.maktaba.player.watch.int
import com.hamzalabs.maktaba.player.watch.json
import com.hamzalabs.maktaba.player.watch.looperTime
import com.hamzalabs.maktaba.player.watch.runUntil
import com.hamzalabs.maktaba.player.watch.settle
import com.hamzalabs.maktaba.player.watch.testPlayer
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.cancel
import kotlinx.coroutines.test.StandardTestDispatcher
import kotlinx.coroutines.test.TestScope
import kotlinx.coroutines.test.advanceTimeBy
import kotlinx.coroutines.test.runCurrent
import kotlinx.coroutines.test.runTest
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonNull
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.contentOrNull
import kotlinx.serialization.json.doubleOrNull
import kotlinx.serialization.json.intOrNull
import kotlinx.serialization.json.jsonArray
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive
import mockwebserver3.MockResponse
import okhttp3.HttpUrl.Companion.toHttpUrl
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import java.time.Instant

/**
 * Cast (FR-38, 48.33; Epic 58 story 58.06, T02) with a fake receiver in place of Media3's
 * `CastPlayer` and a fake Cast session (Google Play services and a receiver on the network are
 * neither on the JVM). Maktaba's receiver is handed a cast grant in the load's custom data and
 * reports the progress itself; a home older than the grant gets the session's signed URL, and
 * the progress still goes out from the phone, read from the receiver.
 */
@RunWith(AndroidJUnit4::class)
class CastTest {
    @get:Rule(order = NoStall.FIRST) val noStall = NoStall()
    @get:Rule val home = FakeHome()
    @get:Rule val codecs: ShadowMediaCodecConfig = ShadowMediaCodecConfig.withAllDefaultSupportedCodecs()

    private val context: Context = ApplicationProvider.getApplicationContext()
    private val scope = CoroutineScope(SupervisorJob() + Dispatchers.Main.immediate)
    private val background = CoroutineScope(SupervisorJob() + Dispatchers.Default)
    private lateinit var streams: FakeStreams
    private var playback: Playback? = null
    private var receiver: FakeCastPlayer? = null
    private val channel = FakeCastChannel()

    /** The home's addresses (`GET /api/discovery/home`): its HTTPS LAN name and the relay. */
    private val discovered = HomeInfo(
        id = "h1",
        urls = listOf("http://192.168.1.20:8088", "https://192-168-1-20.h1.direct.maktaba.tv:8444", "https://h1.relay.maktaba.tv"),
        apiLevel = 1,
    )

    @Before
    fun setUp() {
        streams = FakeStreams(home)
        streams.install()
    }

    @After
    fun tearDown() {
        playback?.stop()
        scope.cancel()
        background.cancel()
    }

    /** The home has the cast grant: the probe is its 422, a video's or a queue's grant is minted. */
    private fun grantsAnswer(refuse: String? = null) {
        home.answer("/api/cast/grants") { r ->
            val body = r.json()
            when {
                body.isEmpty() -> problem(422, "invalid-request", "invalid_request")
                refuse != null -> problem(403, refuse, refuse)
                else -> FakeHome.json(
                    """{"grant":"cast_G1","scope":"cast","expires_at":"${Instant.ofEpochMilli(looperTime()).plusSeconds(6 * 3600)}",""" +
                        body.entries.joinToString(",") { (k, v) -> "\"$k\":$v" } + "}",
                    code = 201,
                )
            }
        }
    }

    private fun problem(status: Int, slug: String, code: String) = MockResponse.Builder().code(status)
        .setHeader("Content-Type", "application/problem+json")
        .body("""{"type":"https://maktaba.tv/problems/$slug","title":"$slug","status":$status,"code":"$code"}""").build()

    private fun sender(discover: HomeInfo? = discovered) = CastSender(
        home.home.client, channel, scope, lang = { "ar" }, appId = "MAKTABA1", nowMs = looperTime, discover = { discover },
    )

    private fun playbackWith(sender: CastSender?): Playback = Playback(
        context, home.home.client, scope, background, signedOut = {}, build = testPlayer(context),
        cast = { local, transfer -> FakeCastPlayer(local, transfer).also { receiver = it } },
        castSender = sender,
        system = false,
        nowMs = looperTime,
    ).also { playback = it }

    // Story 53.08: the playlist carries the session-open call's id as rid (a UUID); the rest
    // of the URL is the signed one, unchanged.
    private fun withoutRid(url: String): String {
        val u = url.toHttpUrl()
        val rid = u.queryParameter("rid")
        assertTrue("rid is the open call's id: $url", rid != null && java.util.UUID.fromString(rid).toString() == rid)
        return u.newBuilder().removeAllQueryParameters("rid").build().toString()
    }

    private val v1 = Video("v1", title = "The Message", durationSec = 4.0)

    private fun Playback.playing(): FakeCastPlayer {
        runUntil { player?.playbackState == Player.STATE_READY }
        return receiver!!
    }

    private fun grantCalls() = home.requests.filter { it.url.encodedPath == "/api/cast/grants" }.map { it.json() }.filter { it.isNotEmpty() }

    // ---------- T02: the custom data (shared/cast/load-vectors.json) ----------

    @Test
    fun everyLoadVectorGivesItsCustomData() {
        val cases = Json.parseToJsonElement(sharedFile("cast/load-vectors.json").readText()).jsonObject.getValue("cases").jsonArray
        assertTrue("the vectors lost their cases", cases.size >= 6)
        for (c in cases) {
            val o = c.jsonObject
            fun s(k: String) = o[k]?.jsonPrimitive?.contentOrNull
            val got = castData(
                CastLoadInput(
                    senderBase = s("sender_base")!!,
                    discoveryUrls = o.getValue("discovery_urls").jsonArray.map { it.jsonPrimitive.content },
                    grant = s("grant")!!,
                    videoId = s("video_id"),
                    queueId = s("queue_id"),
                    queuePos = o["queue_pos"]?.jsonPrimitive?.intOrNull,
                    startSec = o.getValue("start_sec").jsonPrimitive.doubleOrNull!!,
                    lang = s("lang")!!,
                    apiLevel = o.getValue("api_level").jsonPrimitive.intOrNull!!,
                ),
            )
            val expect = o.getValue("expect")
            assertEquals(s("name"), if (expect is JsonNull) null else expect, got)
        }
    }

    @Test
    fun theConverterHandsTheGrantInTheCustomDataAndNothingElse() {
        val data = castData(CastLoadInput("https://h1.relay.maktaba.tv", emptyList(), "cast_G1", videoId = "v1", startSec = 3.0, lang = "ar"))!!
        val item = MediaItem.Builder()
            .setMediaId("v1")
            .setUri("https://h1.relay.maktaba.tv/stream/s1/master.m3u8?sig=x")
            .setMediaMetadata(MediaMetadata.Builder().setTitle("The Message").build())
            .build()
            .withCastData(data)
        val media = CastItems().toMediaQueueItem(item).media!!
        assertEquals("the video names the load", "v1", media.contentId)
        assertNull("no URL: the receiver opens its own session", media.contentUrl)
        assertEquals("The Message", media.metadata!!.getString(CastMetadata.KEY_TITLE))
        val custom = Json.parseToJsonElement(media.customData!!.toString()).jsonObject
        assertEquals(data, custom)
        assertEquals(setOf("home", "urls", "grant", "video_id", "start_sec", "lang", "api_level"), custom.keys)
        assertFalse("never a header or the person's token", media.customData.toString().contains("Bearer") || media.customData.toString().contains("token"))

        // back from the receiver's media status: what it plays, never the grant
        val back = CastItems().toMediaItem(CastItems().toMediaQueueItem(item))
        assertEquals("v1", back.mediaId)
        assertEquals(ReceiverItem("v1", null, null), back.receiverItem)
        assertNull(back.castData!!["grant"])
    }

    @Test
    fun theConverterKeepsTheUrlAndTitleBothWaysForAnOlderHome() {
        val url = "http://192.0.2.20:8088/stream/direct/0b5c7a3e-2f7d-4c55-9d43-6a7c1e0f9a11?sig=abc"
        val item = MediaItem.Builder()
            .setUri(url)
            .setMediaMetadata(MediaMetadata.Builder().setTitle("Omar").build())
            .build()
        val queued = CastItems().toMediaQueueItem(item)
        // direct play names no type: the receiver is told MP4
        assertEquals(MimeTypes.VIDEO_MP4, queued.media!!.contentType)
        assertNull(queued.media!!.customData)
        val back = CastItems().toMediaItem(queued)
        assertEquals(url, back.localConfiguration!!.uri.toString())
        assertEquals("Omar", back.mediaMetadata.title)
    }

    // ---------- no receiver application ID: no Cast ----------

    @Test
    fun withoutAReceiverApplicationIdCastIsOff() {
        assertEquals("shared/cast/app.json names no receiver yet", "", CAST_APP_ID)
        val local = ExoPlayer.Builder(context).build()
        try {
            val noop = CastPlayer.TransferCallback { _, _ -> }
            assertFalse(castStarted(context, appId = ""))
            assertTrue("the phone's player is the one driven", castable(context, local, noop, appId = "") === local)
            assertNull("no sender, no Cast context", CastSenders.of(context, home.home.client, appId = ""))
            assertFalse(isRemote(local))
        } finally {
            local.release()
        }
    }

    @Test
    fun withoutGooglePlayServicesThePhonePlaysAlone() {
        // the JVM has no Play services: Cast is off even with an ID
        val local = ExoPlayer.Builder(context).build()
        try {
            assertTrue(castable(context, local, { _, _ -> }, appId = "MAKTABA1") === local)
        } finally {
            local.release()
        }
    }

    // ---------- Maktaba's receiver, with a grant ----------

    @Test
    fun castingMintsAGrantAndHandsItToMaktabasReceiver() {
        grantsAnswer()
        val s = sender()
        val p = playbackWith(s)
        p.play(v1)
        val cast = p.playing()
        runUntil { s.mode == CastMode.Grant }
        assertEquals("Maktaba's receiver launches", "MAKTABA1", channel.receiver)
        channel.connect()
        cast.startCasting(atMs = 3_000)
        runUntil { cast.handed.isNotEmpty() }

        assertEquals("the grant names the video", listOf("""{"video_id":"v1"}"""), grantCalls().map { it.toString() })
        val grantCall = home.requests.last { it.url.encodedPath == "/api/cast/grants" }
        assertTrue("minted with the person's own sign-in", grantCall.headers["Authorization"]!!.startsWith("Bearer "))
        val media = cast.handed.single().media!!
        assertEquals("v1", media.contentId)
        assertNull(media.contentUrl)
        val custom = Json.parseToJsonElement(media.customData!!.toString()).jsonObject
        assertEquals("https://192-168-1-20.h1.direct.maktaba.tv:8444", custom["home"]!!.jsonPrimitive.content)
        assertEquals("cast_G1", custom["grant"]!!.jsonPrimitive.content)
        assertEquals(3, custom["start_sec"]!!.jsonPrimitive.intOrNull)
        assertEquals("ar", custom["lang"]!!.jsonPrimitive.content)
        assertTrue(p.casting)
        assertTrue("the receiver reports", p.receiverReports)
        // the phone's own session is closed: the receiver opens its own
        runUntil { "s1" in streams.closed() }
    }

    @Test
    fun whileTheReceiverReportsThePhonePostsNoProgress() {
        grantsAnswer()
        val s = sender()
        val p = playbackWith(s)
        p.play(v1)
        val cast = p.playing()
        runUntil { s.mode == CastMode.Grant }
        channel.connect()
        cast.startCasting(atMs = 2_000)
        runUntil { p.receiverReports }
        settle(home, background)
        val before = streams.progress().size

        // the phone locks: 30 s pass with the receiver playing on, and the home hears nothing from the phone
        cast.receiverPlays("v1", atMs = 3_000)
        idleFor(30_000)
        settle(home, background)
        assertEquals("the receiver reports, not the phone", before, streams.progress().size)
        // leaving the player lets the receiver play on, and posts nothing either
        p.stop()
        settle(home, background)
        assertEquals(before, streams.progress().size)
        assertEquals("the cast goes on", 0, channel.ends)
    }

    @Test
    fun aQueueCastNamesTheQueueAndThePhoneFollowsTheTv() {
        grantsAnswer()
        val s = sender()
        val p = playbackWith(s)
        p.castQueue = "q1" to 2
        p.play(v1)
        val cast = p.playing()
        runUntil { s.mode == CastMode.Grant }
        channel.connect()
        cast.startCasting(atMs = 1_000)
        runUntil { cast.handed.isNotEmpty() }
        assertEquals("""{"queue_id":"q1"}""", grantCalls().single().toString())
        val custom = Json.parseToJsonElement(cast.handed.single().media!!.customData!!.toString()).jsonObject
        assertEquals("q1", custom["queue_id"]!!.jsonPrimitive.content)
        assertEquals(2, custom["queue_pos"]!!.jsonPrimitive.intOrNull)
        assertNull("a queue load names no single video", custom["video_id"])

        // the TV walks the queue on its own: the phone shows what it plays
        cast.receiverPlays("v2", atMs = 0, queueId = "q1", queuePos = 3, title = "Omar")
        runUntil { p.video?.id == "v2" }
        assertEquals(ReceiverItem("v2", "q1", 3), p.receiverItem)
        assertEquals("q1" to 3, p.castQueue)
        assertEquals("Omar", p.video?.title)
    }

    @Test
    fun endingTheCastOpensTheVideoHereWhereTheTvWas() {
        grantsAnswer()
        val s = sender()
        val p = playbackWith(s)
        p.play(v1)
        val cast = p.playing()
        runUntil { s.mode == CastMode.Grant }
        channel.connect()
        cast.startCasting(atMs = 1_000)
        runUntil { p.receiverReports }
        cast.receiverPlays("v1", atMs = 2_500)
        val opened = streams.opens().size

        cast.stopCasting()
        runUntil { streams.opens().size > opened && p.player?.playbackState == Player.STATE_READY }
        assertEquals("opened here at the TV's position", 2, streams.opens().last().int("start_sec"))
        assertFalse(p.casting)
        assertFalse(p.receiverReports)
    }

    @Test
    fun aRefusedGrantCastsNothingAndSaysWhy() {
        grantsAnswer(refuse = "access-denied")
        val s = sender()
        val p = playbackWith(s)
        p.play(v1)
        val cast = p.playing()
        runUntil { s.mode == CastMode.Grant }
        channel.connect()
        cast.startCasting(atMs = 1_000)
        runUntil { channel.ends == 1 }
        assertEquals(R.string.cast_error_refused, s.error)
        assertTrue("nothing reached the receiver", cast.handed.isEmpty())
        assertFalse(p.receiverReports)
    }

    @Test
    fun aSignInThroughTheCloudMayNotCast() {
        grantsAnswer(refuse = "cast_via_cloud")
        val s = sender()
        val p = playbackWith(s)
        p.play(v1)
        val cast = p.playing()
        runUntil { s.mode == CastMode.Grant }
        channel.connect()
        cast.startCasting(atMs = 1_000)
        runUntil { channel.ends == 1 }
        assertEquals(R.string.cast_error_cloud, s.error)
    }

    @Test
    fun aHomeNoTvCanReachHasNoCast() {
        grantsAnswer()
        val s = sender(discover = HomeInfo(id = "h1", urls = listOf("http://192.168.1.20:8088"), apiLevel = 1))
        s.start()
        runUntil { s.mode != CastMode.Unknown }
        assertEquals(CastMode.Unreachable, s.mode)
    }

    // ---------- a home older than the cast grant ----------

    @Test
    fun anOlderHomeCastsTheSignedUrlToGooglesReceiver() {
        // no grants route: a plain 404 (FakeHome's own), not the home's JSON problem
        val s = sender()
        val p = playbackWith(s)
        p.play(v1)
        val cast = p.playing()
        runUntil { s.mode == CastMode.OldHome }
        assertEquals("the next session launches the Default Media Receiver", CastMediaControlIntent.DEFAULT_MEDIA_RECEIVER_APPLICATION_ID, channel.receiver)
        channel.connect()
        cast.startCasting(atMs = 3_000)

        assertTrue("the phone says the receiver plays it", p.casting)
        val media = cast.handed.single().media!!
        val signed = home.home.client.home.resolve("/stream/s1/master.m3u8?sig=x").toString()
        assertEquals(signed, withoutRid(media.contentUrl!!))
        assertEquals(media.contentUrl, media.contentId)
        assertEquals(MimeTypes.APPLICATION_M3U8, media.contentType)
        assertEquals(MediaInfo.STREAM_TYPE_BUFFERED, media.streamType)
        assertEquals("The Message", media.metadata!!.getString(CastMetadata.KEY_TITLE))
        assertNull("nothing but the URL and the title: no token, no headers", media.customData)
        assertTrue("no grant was minted", grantCalls().isEmpty())
        assertFalse(p.receiverReports)
    }

    /** Epic 57 story 57.15: the time is over while a receiver plays: the receiver is told to stop, not just let go of. */
    @Test
    fun aTimeRefusalWhileCastingStopsTheReceiver() {
        val p = playbackWith(null)
        p.play(v1)
        val cast = p.playing()
        cast.startCasting(atMs = 3_000)
        assertEquals(0, cast.stops)
        home.answer("/api/stream/sessions/s1/progress") {
            MockResponse.Builder().code(403).setHeader("Content-Type", "application/problem+json")
                .body("""{"type":"https://maktaba.tv/problems/parental-time","title":"over","status":403,"code":"parental_time","reason":"limit_reached"}""").build()
        }
        // the 10-second watch posts the receiver's position; the home answers that the time is over
        idleFor(10_000)
        runUntil { p.timeUp != null }
        assertEquals("the receiver was told to stop", 1, cast.stops)
        assertTrue("and then let go of", cast.released)
        assertNull(p.control)
    }

    @Test
    fun anOlderHomesProgressStillReportsFromTheReceiver() {
        val p = playbackWith(null)
        p.play(v1)
        val cast = p.playing()
        cast.startCasting(atMs = 3_000)
        // the phone's own pause as the receiver took over has been posted and answered
        settle(home, background)
        val before = streams.progress().size

        // the 10-second watch reads the receiver's position, as it plays there
        idleFor(10_000)
        runUntil { streams.progress().size > before }
        val (session, body) = streams.progress().last()
        assertEquals("s1", session)
        assertEquals(3, body.int("position_sec"))

        // closing reports where the receiver was
        cast.remoteAtMs = 3_900
        p.stop()
        runUntil { streams.progress().last().second.int("position_sec") == 3 && "s1" in streams.closed() }
    }

    @Test
    fun aReopenWhileCastingToAnOlderHomeHandsTheReceiverTheNewUrl() {
        streams.ladder = """[{"height":720,"width":1280,"bitrate_kbps":3000,"codec":"h264"}]"""
        val p = playbackWith(null)
        p.play(v1)
        val cast = p.playing()
        cast.startCasting(atMs = 2_000)

        p.setQuality(3000)
        runUntil { cast.handed.size == 2 }
        assertEquals(home.home.client.home.resolve("/stream/s2/master.m3u8?sig=x").toString(),
            withoutRid(cast.handed.last().media!!.contentUrl!!))
        assertEquals(2, streams.opens().last().int("start_sec"))
    }
}

/** The grant's refresh and the receiver's messages, on a test clock. */
@RunWith(AndroidJUnit4::class)
class CastGrantTest {
    @get:Rule val home = FakeHome()

    @Test
    fun aFreshGrantGoesToTheReceiverBeforeTheFirstEnds() = runTest(StandardTestDispatcher()) {
        val t0 = 1_794_000_000_000L
        val channel = FakeCastChannel()
        var minted = 0
        val sender = CastSender(
            home.home.client, channel, backgroundScope, lang = { "en" }, appId = "MAKTABA1",
            nowMs = { t0 + testScheduler.currentTime },
            discover = { HomeInfo(id = "h1", urls = listOf("https://h1.relay.maktaba.tv"), apiLevel = 1) },
            call = { _, _, body ->
                if (body == "{}") 422 to """{"type":"x","title":"x","status":422,"code":"invalid_request"}"""
                else {
                    minted++
                    // each grant lasts 40 minutes: the next is minted 10 minutes in
                    201 to """{"grant":"cast_G$minted","scope":"cast","expires_at":"${Instant.ofEpochMilli(t0 + testScheduler.currentTime + 40 * 60_000)}"}"""
                }
            },
        )
        val item = MediaItem.Builder().setMediaId("v1").build()
        channel.connect()
        val load = sender.load(item, CastTarget("v1"), startMs = 0)
        assertTrue(load is CastLoad.Grant)
        assertEquals("cast_G1", (load as CastLoad.Grant).item.castData!!["grant"]!!.jsonPrimitive.content)

        advanceTimeBy(10 * 60_000L - 1_000)
        runCurrent()
        assertTrue("not yet", channel.sent.isEmpty())
        advanceTimeBy(2_000)
        runCurrent()
        assertEquals(listOf("""{"type":"grant","grant":"cast_G2"}"""), channel.sent)

        // the cast ends: nothing is renewed any more
        channel.end()
        advanceTimeBy(60 * 60_000L)
        runCurrent()
        assertEquals(1, channel.sent.size)
    }

    @Test
    fun aHomeThatCouldNotBeAskedIsAskedAgainAtTheNextCast() = runTest {
        var asked = 0
        val sender = CastSender(
            home.home.client, FakeCastChannel(), backgroundScope, lang = { "en" }, appId = "MAKTABA1",
            discover = { HomeInfo(id = "h1", urls = listOf("https://h1.relay.maktaba.tv"), apiLevel = 1) },
            call = { _, _, _ ->
                asked++
                if (asked == 1) throw com.hamzalabs.maktaba.core.api.ApiException.Transport("offline")
                422 to """{"type":"x","title":"x","status":422,"code":"invalid_request"}"""
            },
        )
        sender.start().join()
        assertEquals("not known: no Cast mode is guessed", CastMode.Unknown, sender.mode)
        val load = sender.load(MediaItem.Builder().setMediaId("v1").build(), CastTarget("v1"), 0)
        // asked again before the load: the home has the grant now, the mint is the third call
        assertEquals(CastMode.Grant, sender.mode)
        assertTrue(load is CastLoad.Refused)
        assertEquals(R.string.cast_error_failed, sender.error)
        assertEquals(3, asked)
    }

    @Test
    fun theReceiversErrorsShow() = runTest {
        val sender = CastSender(home.home.client, FakeCastChannel(), backgroundScope, lang = { "en" }, appId = "MAKTABA1")
        sender.heard("""{"type":"error","error":"home"}""")
        assertEquals(R.string.cast_error_home, sender.error)
        sender.heard("""{"type":"error","error":"grant"}""")
        assertEquals(R.string.cast_error_grant_ended, sender.error)
        sender.heard("""{"type":"state","state":"playing"}""")
        assertEquals("a state is not an error", R.string.cast_error_grant_ended, sender.error)
        sender.heard("not json")
        sender.clearError()
        assertNull(sender.error)
    }
}
