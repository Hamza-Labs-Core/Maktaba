package com.hamzalabs.maktaba.desktop.mpv

import com.hamzalabs.maktaba.core.MapStore
import com.hamzalabs.maktaba.desktop.Lang
import com.hamzalabs.maktaba.desktop.Players
import com.hamzalabs.maktaba.desktop.Video
import com.hamzalabs.maktaba.desktop.VideoEvents
import com.hamzalabs.maktaba.desktop.helpUrl
import com.hamzalabs.maktaba.desktop.shell.AppPrefs
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertSame
import org.junit.Assert.assertTrue
import org.junit.Test
import java.io.File
import java.time.Instant

/**
 * Story 13.21: the engine is chosen at each open by the variable, then Profile's setting, then
 * whether libmpv loaded (T01); the notice is said once per version and reason (T03); a stored
 * choice this Player cannot play reads as Automatic (T05); the problem report names the engine
 * of each video and why.
 */
class EngineTest {
    private val lib = java.lang.reflect.Proxy.newProxyInstance(LibMpv::class.java.classLoader, arrayOf(LibMpv::class.java)) { _, _, _ -> null } as LibMpv

    private object FakeMpv : Players {
        override val profile = MPV_PROFILE
        override suspend fun open(url: String, fromMs: Long, autoplay: Boolean, events: VideoEvents): Video = error("not opened here")
    }

    private object FakeFx : Players {
        override suspend fun open(url: String, fromMs: Long, autoplay: Boolean, events: VideoEvents): Video = error("not opened here")
    }

    private val loaded = MpvLoad.Loaded(lib, File("/opt/maktaba-player/lib/app/libmpv.so.2"))
    private val missing = MpvLoad.Missing("libmpv-2.dll is not in C:\\Maktaba Player\\app")

    private fun engines(load: MpvLoad, env: Map<String, String> = emptyMap(), choice: EngineChoice = EngineChoice.Auto) =
        Engines(env, load = { load }, mpv = { FakeMpv }, fx = FakeFx).apply { setting = { choice } }

    @Test
    fun t01_theVariableThenTheSettingThenLibmpv() {
        val variable = engines(loaded, env = mapOf("MAKTABA_PLAYER_ENGINE" to "JavaFX"))
        assertSame(FakeFx, variable.players())
        assertTrue(variable.describe(), variable.describe().contains("MAKTABA_PLAYER_ENGINE"))

        val profile = engines(loaded, choice = EngineChoice.Older)
        assertSame(FakeFx, profile.players())
        assertTrue(profile.describe(), profile.describe().contains("chosen in Profile"))

        val auto = engines(loaded)
        assertSame(FakeMpv, auto.players())
        assertTrue(auto.describe(), auto.describe().contains("libmpv, automatic") && auto.describe().contains("libmpv.so.2"))

        val gone = engines(missing)
        assertSame(FakeFx, gone.players())
        assertTrue(gone.describe(), gone.describe().contains("libmpv did not load: libmpv-2.dll is not in"))
    }

    @Test
    fun theSettingIsReadAtEachOpen() {
        var choice = EngineChoice.Auto
        val e = Engines(emptyMap(), load = { loaded }, mpv = { FakeMpv }, fx = FakeFx).apply { setting = { choice } }
        assertSame(FakeMpv, e.players())
        choice = EngineChoice.Older
        assertSame(FakeFx, e.players())
        choice = EngineChoice.Auto
        assertSame(FakeMpv, e.players())
    }

    @Test
    fun theOlderEngineChosenOnPurposeDoesNotLoadLibmpv() {
        var asked = 0
        val e = Engines(emptyMap(), load = { asked++; loaded }, mpv = { FakeMpv }, fx = FakeFx).apply { setting = { EngineChoice.Older } }
        e.players()
        assertEquals(0, asked)
    }

    @Test
    fun theProblemReportAndTheRowDoNotLoadLibmpvForTheOlderEngine() {
        var asked = 0
        fun of(env: Map<String, String>, choice: EngineChoice) =
            Engines(env, load = { asked++; loaded }, mpv = { FakeMpv }, fx = FakeFx).apply { setting = { choice } }
        // the variable: whatever is chosen
        val variable = of(mapOf("MAKTABA_PLAYER_ENGINE" to "javafx"), EngineChoice.Auto)
        assertEquals("playback: JavaFX, MAKTABA_PLAYER_ENGINE", variable.describe())
        assertEquals(Engines.Why.Variable, variable.row().lock)
        assertEquals(Engines.Why.Variable, variable.rowNow(EngineChoice.Auto)!!.lock)
        // the older engine chosen in Profile, also after a video and in the report's lines
        val older = of(emptyMap(), EngineChoice.Older)
        assertEquals("playback: JavaFX, chosen in Profile", older.describe())
        older.players()
        older.logLines()
        assertEquals(Engines.Row(EngineChoice.Older), older.row())
        assertEquals(Engines.Row(EngineChoice.Older), older.rowNow(EngineChoice.Older))
        assertEquals("libmpv was loaded", 0, asked)
        // Automatic needs it, but not at once on the screen; once looked for, it is known
        assertNull(older.rowNow(EngineChoice.Auto))
        assertEquals(0, asked)
        assertEquals(Engines.Row(EngineChoice.Auto), older.row(EngineChoice.Auto))
        assertEquals(1, asked)
        assertEquals(Engines.Row(EngineChoice.Auto), older.rowNow(EngineChoice.Auto))
    }

    @Test
    fun theProbeRunsOnceFromTheFirstTimeLibmpvPlaysAndNeverWhileJavaFxIsChosen() {
        var choice = EngineChoice.Older
        val asked = java.util.concurrent.atomic.AtomicInteger()
        val probes = java.util.concurrent.LinkedBlockingQueue<MpvLoad.Loaded>()
        val e = Engines(emptyMap(), load = { asked.incrementAndGet(); loaded }, mpv = { FakeMpv }, fx = FakeFx, probe = { probes += it }).apply { setting = { choice } }
        // at the start, the older engine chosen: nothing is probed, nor is libmpv loaded
        e.startProbe()
        assertNull(probes.poll(300, java.util.concurrent.TimeUnit.MILLISECONDS))
        assertEquals(0, asked.get())
        // Automatic chosen later: probed then, once
        choice = EngineChoice.Auto
        e.startProbe()
        assertSame(loaded, probes.poll(10, java.util.concurrent.TimeUnit.SECONDS))
        e.startProbe()
        choice = EngineChoice.Older
        e.startProbe()
        choice = EngineChoice.Auto
        e.startProbe()
        assertNull(probes.poll(300, java.util.concurrent.TimeUnit.MILLISECONDS))
        // a libmpv that did not load, or the variable: never
        val never = java.util.concurrent.LinkedBlockingQueue<MpvLoad.Loaded>()
        Engines(emptyMap(), load = { missing }, mpv = { FakeMpv }, fx = FakeFx, probe = { never += it }).startProbe()
        Engines(mapOf("MAKTABA_PLAYER_ENGINE" to "javafx"), load = { loaded }, mpv = { FakeMpv }, fx = FakeFx, probe = { never += it }).startProbe()
        assertNull(never.poll(300, java.util.concurrent.TimeUnit.MILLISECONDS))
    }

    @Test
    fun theRowShowsTheChoiceOrWhyItIsLocked() {
        assertEquals(Engines.Row(EngineChoice.Auto), engines(loaded).row())
        assertEquals(EngineChoice.Older, engines(loaded).row(EngineChoice.Older).shown)
        val variable = engines(loaded, env = mapOf("MAKTABA_PLAYER_ENGINE" to "javafx")).row()
        assertEquals(Engines.Why.Variable, variable.lock)
        assertEquals(EngineChoice.Older, variable.shown)
        val gone = engines(missing).row()
        assertEquals(Engines.Why.Missing, gone.lock)
        assertEquals(EngineChoice.Older, gone.shown)
        assertEquals(missing.reason, gone.reason)
    }

    @Test
    fun theNoticeIsForAnInstalledPlayerWhoseLibmpvDidNotLoad() {
        assertEquals(missing.reason, engines(missing).noticeReason(installed = true))
        assertNull("a development run", engines(missing).noticeReason(installed = false))
        assertNull("libmpv plays", engines(loaded).noticeReason(installed = true))
        assertNull("chosen in Profile", engines(missing, choice = EngineChoice.Older).noticeReason(installed = true))
        assertNull("the variable", engines(missing, env = mapOf("MAKTABA_PLAYER_ENGINE" to "javafx")).noticeReason(installed = true))
    }

    @Test
    fun t03_theNoticeIsSaidOncePerVersionAndReason() {
        val store = MapStore()
        val first = EngineNotice(store, "0.4.1")
        assertEquals("why", first.pending("why"))
        first.said("why")
        // the next start, the same version and reason: nothing
        assertNull(EngineNotice(store, "0.4.1").pending("why"))
        // another reason, or a new version: said again
        assertEquals("another", EngineNotice(store, "0.4.1").pending("another"))
        assertEquals("why", EngineNotice(store, "0.4.2").pending("why"))
        assertNull(EngineNotice(store, "0.4.2").pending(null))
    }

    @Test
    fun t05_aStoredChoiceThisPlayerCannotPlayReadsAsAutomatic() {
        assertEquals(EngineChoice.Older, EngineChoice.of("javafx"))
        // after story 13.22 the Player has no older engine
        assertEquals(EngineChoice.Auto, EngineChoice.of("javafx", olderAvailable = false))
        // a newer Player's value, nothing, or rubbish
        assertEquals(EngineChoice.Auto, EngineChoice.of("vulkan"))
        assertEquals(EngineChoice.Auto, EngineChoice.of(null))
        assertEquals(EngineChoice.Auto, EngineChoice.of(""))
    }

    @Test
    fun theChoiceIsKeptInThePlayersOwnSettings() {
        val store = MapStore()
        AppPrefs(store).chooseEngine(EngineChoice.Older)
        assertEquals("javafx", store.get("player.engine"))
        assertEquals(EngineChoice.Older, AppPrefs(store).engine)
        // not in the shared playback settings
        assertTrue(store.get("playback.engine") == null)
    }

    @Test
    fun theProblemReportNamesEachVideosEngineAndWhy() {
        var choice = EngineChoice.Auto
        val e = Engines(emptyMap(), load = { loaded }, mpv = { FakeMpv }, fx = FakeFx).apply { setting = { choice } }
        e.players(Instant.parse("2026-11-24T10:00:00Z"))
        choice = EngineChoice.Older
        e.players(Instant.parse("2026-11-24T10:05:00Z"))
        val lines = e.logLines(Instant.parse("2026-11-24T10:06:00Z"))
        assertEquals(3, lines.size)
        assertTrue(lines[0], lines[0].contains("2026-11-24T10:00:00Z") && lines[0].contains("libmpv, automatic"))
        assertTrue(lines[1], lines[1].contains("JavaFX, chosen in Profile"))
        assertTrue(lines[2], lines[2].contains("\"msg\":\"playback: JavaFX, chosen in Profile\""))
    }

    // story 13.20: playback's other lines (the home's subtitles not read) go in the same log
    @Test
    fun aNoteIsInTheProblemReportsLog() {
        val e = engines(loaded)
        e.note("subtitles: the home's list of v1 was not read: HTTP 500", Instant.parse("2026-11-24T10:00:00Z"))
        val lines = e.logLines(Instant.parse("2026-11-24T10:01:00Z"))
        assertEquals(2, lines.size)
        assertTrue(lines[0], lines[0].contains("2026-11-24T10:00:00Z") && lines[0].contains("subtitles: the home's list of v1 was not read"))
        // only the newest are kept
        repeat(Engines.KEPT + 5) { e.note("line $it") }
        assertEquals(Engines.KEPT + 1, e.logLines().size)
    }

    @Test
    fun learnMoreOpensTheHomesHelpInThePlayersLanguage() {
        assertEquals("http://192.0.2.20:8088/help/desktop-player?lang=ar#video-engine", helpUrl("http://192.0.2.20:8088/", Lang.Ar, "video-engine"))
        assertEquals("https://home.example/m/help/desktop-player?lang=en#video-engine", helpUrl("https://home.example/m", Lang.En, "video-engine"))
        // no home signed in: maktaba.dev's Player page
        assertEquals("https://maktaba.dev/apps/player/", helpUrl(null, Lang.En, "video-engine"))
        assertEquals("https://maktaba.dev/ar/apps/player/", helpUrl("", Lang.Ar))
    }
}
