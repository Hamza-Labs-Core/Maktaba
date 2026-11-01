@file:OptIn(UnstableApi::class)

package com.hamzalabs.maktaba.player.media

import android.content.Context
import androidx.annotation.OptIn
import androidx.media3.common.DeviceInfo
import androidx.media3.common.ForwardingPlayer
import androidx.media3.common.MediaItem
import androidx.media3.common.MediaMetadata
import androidx.media3.common.MimeTypes
import androidx.media3.common.Player
import androidx.media3.common.util.UnstableApi
import androidx.media3.exoplayer.ExoPlayer
import androidx.media3.test.utils.robolectric.ShadowMediaCodecConfig
import androidx.test.core.app.ApplicationProvider
import androidx.test.ext.junit.runners.AndroidJUnit4
import com.google.android.gms.cast.MediaInfo
import com.google.android.gms.cast.MediaMetadata as CastMetadata
import com.hamzalabs.maktaba.core.api.Video
import com.hamzalabs.maktaba.player.Playback
import com.hamzalabs.maktaba.player.harness.FakeHome
import com.hamzalabs.maktaba.player.watch.FakeStreams
import com.hamzalabs.maktaba.player.watch.idleFor
import com.hamzalabs.maktaba.player.watch.int
import com.hamzalabs.maktaba.player.watch.runUntil
import com.hamzalabs.maktaba.player.watch.testPlayer
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.cancel
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith

/**
 * Cast (FR-38, 48.33) with a fake receiver in place of Media3's `CastPlayer` (which needs Google
 * Play services and a receiver on the network): the receiver is handed the session's signed URL
 * and the title, and the progress still goes out from the phone, read from the receiver.
 */
@RunWith(AndroidJUnit4::class)
class CastTest {
    @get:Rule val home = FakeHome()
    @get:Rule val codecs: ShadowMediaCodecConfig = ShadowMediaCodecConfig.withAllDefaultSupportedCodecs()

    private val context: Context = ApplicationProvider.getApplicationContext()
    private val scope = CoroutineScope(SupervisorJob() + Dispatchers.Main.immediate)
    private val background = CoroutineScope(SupervisorJob() + Dispatchers.Default)
    private lateinit var streams: FakeStreams
    private lateinit var playback: Playback
    private var receiver: FakeCastPlayer? = null

    @Before
    fun setUp() {
        streams = FakeStreams(home)
        streams.install()
        playback = Playback(
            context, home.home.client, scope, background, signedOut = {}, build = testPlayer(context),
            cast = { local -> FakeCastPlayer(local).also { receiver = it } },
            system = false,
        )
    }

    @After
    fun tearDown() {
        playback.stop()
        scope.cancel()
        background.cancel()
    }

    private val v1 = Video("v1", title = "The Message", durationSec = 4.0)

    private fun playing(): FakeCastPlayer {
        runUntil { playback.player?.playbackState == Player.STATE_READY }
        return receiver!!
    }

    @Test
    fun theReceiverIsHandedTheSignedUrlAndTheTitle() {
        playback.play(v1)
        val cast = playing()
        cast.startCasting(atMs = 3_000)

        assertTrue("the phone says the receiver plays it", playback.casting)
        val media = cast.handed.single().media!!
        val signed = home.home.client.home.resolve("/stream/s1/master.m3u8?sig=x").toString()
        assertEquals(signed, media.contentUrl)
        assertEquals(signed, media.contentId)
        assertEquals(MimeTypes.APPLICATION_M3U8, media.contentType)
        assertEquals(MediaInfo.STREAM_TYPE_BUFFERED, media.streamType)
        assertEquals("The Message", media.metadata!!.getString(CastMetadata.KEY_TITLE))
        assertNull("nothing but the URL and the title: no token, no headers", media.customData)
    }

    @Test
    fun theProgressStillReportsFromTheReceiver() {
        playback.play(v1)
        val cast = playing()
        cast.startCasting(atMs = 3_000)
        val before = streams.progress().size

        // the 10-second watch reads the receiver's position, as it plays there
        idleFor(10_000)
        runUntil { streams.progress().size > before }
        val (session, body) = streams.progress().last()
        assertEquals("s1", session)
        assertEquals(3, body.int("position_sec"))

        // closing reports where the receiver was
        cast.remoteAtMs = 3_900
        playback.stop()
        runUntil { streams.progress().last().second.int("position_sec") == 3 && "s1" in streams.closed() }
    }

    @Test
    fun aReopenWhileCastingHandsTheReceiverTheNewUrl() {
        streams.ladder = """[{"height":720,"width":1280,"bitrate_kbps":3000,"codec":"h264"}]"""
        playback.play(v1)
        val cast = playing()
        cast.startCasting(atMs = 2_000)

        playback.setQuality(3000)
        runUntil { cast.handed.size == 2 }
        assertEquals(home.home.client.home.resolve("/stream/s2/master.m3u8?sig=x").toString(), cast.handed.last().media!!.contentUrl)
        assertEquals(2, streams.opens().last().int("start_sec"))
    }

    @Test
    fun theConverterKeepsTheUrlAndTitleBothWays() {
        val url = "http://192.168.1.20:8088/stream/direct/0b5c7a3e-2f7d-4c55-9d43-6a7c1e0f9a11?sig=abc"
        val item = MediaItem.Builder()
            .setUri(url)
            .setMediaMetadata(MediaMetadata.Builder().setTitle("Omar").build())
            .build()
        val queued = CastItems().toMediaQueueItem(item)
        // direct play names no type: the receiver is told MP4
        assertEquals(MimeTypes.VIDEO_MP4, queued.media!!.contentType)
        val back = CastItems().toMediaItem(queued)
        assertEquals(url, back.localConfiguration!!.uri.toString())
        assertEquals("Omar", back.mediaMetadata.title)
    }

    @Test
    fun withoutGooglePlayServicesThePhonePlaysAlone() {
        // the JVM has no Play services: Cast is off, the phone's player is the one driven
        val local = ExoPlayer.Builder(context).build()
        try {
            assertTrue(castable(context, local) === local)
            assertFalse(isRemote(local))
        } finally {
            local.release()
        }
    }
}

/**
 * Media3's `CastPlayer` as the tests need it: the phone's player until [startCasting], then a
 * receiver at [remoteAtMs], playing. [handed] is what the receiver was given, through the app's
 * converter (the item Media3 would turn into the receiver's queue).
 */
class FakeCastPlayer(private val local: ExoPlayer) : ForwardingPlayer(local) {
    private var casting = false
    var remoteAtMs = 0L
    val handed = mutableListOf<com.google.android.gms.cast.MediaQueueItem>()
    private val listeners = mutableListOf<Player.Listener>()

    fun startCasting(atMs: Long) {
        remoteAtMs = atMs
        handed += CastItems().toMediaQueueItem(local.currentMediaItem!!)
        // the phone stops as the receiver takes over (Media3's transfer)
        local.pause()
        casting = true
        listeners.toList().forEach {
            it.onDeviceInfoChanged(deviceInfo)
            it.onIsPlayingChanged(true)
        }
    }

    override fun addListener(listener: Player.Listener) {
        listeners += listener
        super.addListener(listener)
    }

    override fun removeListener(listener: Player.Listener) {
        listeners -= listener
        super.removeListener(listener)
    }

    override fun getDeviceInfo(): DeviceInfo =
        if (casting) DeviceInfo.Builder(DeviceInfo.PLAYBACK_TYPE_REMOTE).build() else super.getDeviceInfo()

    override fun isPlaying(): Boolean = if (casting) true else super.isPlaying()
    override fun getPlayWhenReady(): Boolean = if (casting) true else super.getPlayWhenReady()
    override fun getPlaybackState(): Int = if (casting) Player.STATE_READY else super.getPlaybackState()
    override fun getCurrentPosition(): Long = if (casting) remoteAtMs else super.getCurrentPosition()

    override fun setMediaItem(mediaItem: MediaItem, startPositionMs: Long) {
        if (casting) {
            handed += CastItems().toMediaQueueItem(mediaItem)
            remoteAtMs = startPositionMs
        } else {
            super.setMediaItem(mediaItem, startPositionMs)
        }
    }

    override fun prepare() { if (!casting) super.prepare() }
    override fun setPlayWhenReady(playWhenReady: Boolean) { if (!casting) super.setPlayWhenReady(playWhenReady) }
}
