@file:OptIn(UnstableApi::class)

package com.hamzalabs.maktaba.player.watch

import com.hamzalabs.maktaba.player.profile.SubtitleLanguage
import com.hamzalabs.maktaba.player.profile.PlaybackValues
import android.content.Context
import androidx.annotation.OptIn
import androidx.media3.common.C
import androidx.media3.common.Player
import androidx.media3.common.Tracks
import androidx.media3.common.util.UnstableApi
import androidx.media3.exoplayer.ExoPlayer
import androidx.test.core.app.ApplicationProvider
import androidx.test.ext.junit.runners.AndroidJUnit4
import androidx.media3.test.utils.robolectric.ShadowMediaCodecConfig
import com.hamzalabs.maktaba.core.api.Video
import com.hamzalabs.maktaba.player.Playback
import com.hamzalabs.maktaba.player.harness.FakeHome
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.cancel
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith

/**
 * The player against the fake home's streams, played for real (48.32): resume, progress,
 * sound (FR-39a), subtitles and audio tracks (FR-34), quality (FR-35), the choice kept for the
 * next video.
 */
@RunWith(AndroidJUnit4::class)
class PlaybackTest {
    @get:Rule val home = FakeHome()
    @get:Rule val codecs: ShadowMediaCodecConfig = ShadowMediaCodecConfig.withAllDefaultSupportedCodecs()

    private val context: Context = ApplicationProvider.getApplicationContext()
    private val scope = CoroutineScope(SupervisorJob() + Dispatchers.Main.immediate)
    private val background = CoroutineScope(SupervisorJob() + Dispatchers.Default)
    private lateinit var streams: FakeStreams
    private lateinit var playback: Playback

    @Before
    fun setUp() {
        streams = FakeStreams(home)
        streams.install()
        playback = Playback(context, home.home.client, scope, background, signedOut = {}, build = testPlayer(context))
    }

    @After
    fun tearDown() {
        playback.stop()
        scope.cancel()
        background.cancel()
    }

    private val v1 = Video("v1", title = "The Message", durationSec = 4.0)
    private val v2 = Video("v2", title = "Omar, Episode 1", durationSec = 4.0)

    private fun playing(): ExoPlayer {
        runUntil { playback.player?.playbackState == Player.STATE_READY && playback.tracks.groups.isNotEmpty() }
        return playback.player!!
    }

    @Test
    fun aStreamWithSoundPlaysWithAnAudioTrackSelected() {
        playback.play(v1)
        val p = playing()
        runUntil { p.currentPosition > 500 }
        assertTrue("an audio track is selected", p.currentTracks.isTypeSelected(C.TRACK_TYPE_AUDIO))
        assertNotNull("the audio renderer plays a format", p.audioFormat)
        assertEquals("audio/mp4a-latm", p.audioFormat!!.sampleMimeType)
        assertTrue("the volume is up", p.volume > 0f)
        runUntil { (p.audioDecoderCounters?.renderedOutputBufferCount ?: 0) > 0 }
    }

    @Test
    fun soundIsSelectedEvenWhenAudioWasTurnedOff() {
        playback.play(v1)
        val p = playing()
        var changes = 0
        p.addListener(object : Player.Listener {
            override fun onTracksChanged(tracks: Tracks) { changes++ }
        })
        // something turned audio off (a kept choice, a setting): the player turns it back on
        p.trackSelectionParameters = p.trackSelectionParameters.buildUpon().setTrackTypeDisabled(C.TRACK_TYPE_AUDIO, true).build()
        runUntil { changes >= 2 }
        runUntil { p.currentTracks.isTypeSelected(C.TRACK_TYPE_AUDIO) && p.audioFormat != null }
        assertFalse(p.trackSelectionParameters.disabledTrackTypes.contains(C.TRACK_TYPE_AUDIO))
    }

    @Test
    fun opensAtTheResumePositionAndReportsProgress() {
        playback.play(v1, startSec = 2)
        val p = playing()
        assertEquals(2, streams.opens().single().int("start_sec"))
        assertTrue("starts at the resume position: ${p.currentPosition}", p.currentPosition >= 2_000)

        // paused: the position is reported at once
        p.pause()
        runUntil { streams.progress().isNotEmpty() }
        val (session, body) = streams.progress().last()
        assertEquals("s1", session)
        assertTrue(body.toString(), body.int("position_sec")!! >= 2)

        // played to the end: "completed"
        p.play()
        runUntil { streams.progress().any { it.second["completed"] != null } }
    }

    @Test
    fun reportsProgressEveryTenSecondsWhilePlaying() {
        playback.play(v1)
        val p = playing()
        runUntil { p.currentPosition > 500 }
        val before = streams.progress().size
        // the stream ends in 4 s: keep it playing by seeking back as the watch's clock moves
        p.repeatMode = Player.REPEAT_MODE_ONE
        idleFor(10_000)
        runUntil { streams.progress().size > before }
    }

    @Test
    fun theSubtitleRenditionsAreListedAndAChoiceShows() {
        playback.play(v1)
        playing()
        val subs = trackOptions(playback.tracks, C.TRACK_TYPE_TEXT)
        assertEquals(listOf("English", "العربية"), subs.map { it.label })
        assertEquals(listOf("en", "ar"), subs.map { it.language })
        // off by default (Settings: subtitles off)
        assertTrue(subs.none { it.selected })

        playback.select(subs[1])
        runUntil { trackOptions(playback.tracks, C.TRACK_TYPE_TEXT).singleOrNull { it.selected }?.language == "ar" }
        assertEquals(TrackChoice(textOff = false, textLanguage = "ar"), playback.choice)

        playback.subtitlesOff()
        runUntil { trackOptions(playback.tracks, C.TRACK_TYPE_TEXT).none { it.selected } }
        assertEquals(true, playback.choice.textOff)
    }

    @Test
    fun subtitlesOnInSettingsStartInTheirLanguage() {
        playback.prefs = PlaybackValues(subtitlesOn = true, subtitleLanguage = SubtitleLanguage.Arabic)
        playback.play(v1)
        playing()
        runUntil { trackOptions(playback.tracks, C.TRACK_TYPE_TEXT).singleOrNull { it.selected }?.language == "ar" }
    }

    @Test
    fun audioTracksSwitchAndSoundStaysOn() {
        streams.direct = true
        playback.play(v1)
        val p = playing()
        val audio = trackOptions(playback.tracks, C.TRACK_TYPE_AUDIO)
        assertEquals(listOf("en", "ar"), audio.map { it.language })
        assertEquals("the first is playing", listOf(true, false), audio.map { it.selected })

        playback.select(audio[1])
        runUntil { trackOptions(playback.tracks, C.TRACK_TYPE_AUDIO).singleOrNull { it.selected }?.language == "ar" }
        assertEquals("ar", p.audioFormat?.language)
        assertEquals("ar", playback.choice.audioLanguage)
    }

    @Test
    fun theChoiceIsKeptForTheNextVideo() {
        streams.direct = true
        playback.play(v1)
        playing()
        playback.select(trackOptions(playback.tracks, C.TRACK_TYPE_AUDIO)[1])
        runUntil { playback.choice.audioLanguage == "ar" }

        playback.play(v2)
        runUntil { playback.video?.id == "v2" && playback.player?.playbackState == Player.STATE_READY }
        runUntil { trackOptions(playback.tracks, C.TRACK_TYPE_AUDIO).singleOrNull { it.selected }?.language == "ar" }
        assertEquals("v2", streams.opens().last()["video_id"]!!.toString().trim('"'))
        // the first video's session was closed
        runUntil { "s1" in streams.closed() }
    }

    @Test
    fun aVideoStartsAtTheSettingsQualityUntilOneIsChosenInThePlayer() {
        playback.startAt(1_500)
        playback.play(v1)
        playing()
        assertEquals(1_500, streams.opens().last().int("max_bitrate_kbps"))

        // chosen in the player (Auto): kept for the next video, over the Settings'
        playback.setQuality(null)
        playback.startAt(4_000)
        playback.play(v2)
        runUntil { playback.video?.id == "v2" && playback.player?.playbackState == Player.STATE_READY }
        assertEquals(null, streams.opens().last().int("max_bitrate_kbps"))
    }

    @Test
    fun aQualityReopensAtTheSamePositionWithItsCap() {
        streams.ladder = """[{"height":1080,"width":1920,"bitrate_kbps":6000,"codec":"h264"},{"height":720,"width":1280,"bitrate_kbps":3000,"codec":"h264"}]"""
        playback.play(v1)
        val p = playing()
        assertEquals(listOf(1080, 720), playback.ladder.map { it.height })
        assertEquals(listOf(null, 1080, 720), QualityOption.of(playback.ladder).map { it.height })
        p.pause()
        p.seekTo(2_500)
        runUntil { p.currentPosition >= 2_500 && p.playbackState == Player.STATE_READY }

        playback.setQuality(3000)
        // the old session closes only once the new one plays
        runUntil { "s1" in streams.closed() }
        runUntil { playback.player?.playbackState == Player.STATE_READY }
        assertEquals(2, streams.opens().size)
        val reopened = streams.opens()[1]
        assertEquals(3000, reopened.int("max_bitrate_kbps"))
        assertEquals(2, reopened.int("start_sec"))
        assertEquals(3000, playback.maxBitrateKbps)
        assertTrue(playback.player!!.currentMediaItem!!.localConfiguration!!.uri.toString(), playback.player!!.currentMediaItem!!.localConfiguration!!.uri.path!!.startsWith("/stream/s2/"))
        assertTrue("same position: ${playback.player!!.currentPosition}", playback.player!!.currentPosition >= 2_000)
        assertFalse("still paused", playback.player!!.playWhenReady)

        // Auto: no cap
        playback.setQuality(null)
        runUntil { streams.opens().size == 3 }
        assertEquals(null, streams.opens()[2].int("max_bitrate_kbps"))
    }
}
