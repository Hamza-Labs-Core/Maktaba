package com.hamzalabs.maktaba.desktop.profile

import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.size
import androidx.compose.ui.Modifier
import androidx.compose.ui.test.ExperimentalTestApi
import androidx.compose.ui.test.assertIsEnabled
import androidx.compose.ui.test.assertIsNotEnabled
import androidx.compose.ui.test.assertIsNotSelected
import androidx.compose.ui.test.assertIsSelected
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import androidx.compose.ui.test.performScrollTo
import androidx.compose.ui.test.runComposeUiTest
import androidx.compose.ui.unit.dp
import com.hamzalabs.maktaba.desktop.InPlayer
import com.hamzalabs.maktaba.desktop.Lang
import com.hamzalabs.maktaba.desktop.Players
import com.hamzalabs.maktaba.desktop.TestHome
import com.hamzalabs.maktaba.desktop.Video
import com.hamzalabs.maktaba.desktop.VideoEvents
import com.hamzalabs.maktaba.desktop.mpv.EngineChoice
import com.hamzalabs.maktaba.desktop.mpv.Engines
import com.hamzalabs.maktaba.desktop.mpv.LibMpv
import com.hamzalabs.maktaba.desktop.mpv.MpvLoad
import org.junit.Assert.assertEquals
import org.junit.Test
import java.io.File

/**
 * Story 13.21 T04: Profile → Playback's Video engine row in its four states (Automatic, Older
 * engine, set by the variable, libmpv missing), choosing, and Learn more.
 */
@OptIn(ExperimentalTestApi::class)
class EngineRowTest {
    private val lib = java.lang.reflect.Proxy.newProxyInstance(LibMpv::class.java.classLoader, arrayOf(LibMpv::class.java)) { _, _, _ -> null } as LibMpv
    private val none = Players { _: String, _: Long, _: Boolean, _: VideoEvents -> error("not opened here") as Video }
    private val loaded = MpvLoad.Loaded(lib, File("libmpv.so.2"))
    private val missing = MpvLoad.Missing("libmpv-2.dll is not in C:\\Maktaba Player\\app")

    private fun engines(load: MpvLoad, env: Map<String, String> = emptyMap()) = Engines(env, load = { load }, mpv = { none }, fx = none)

    private fun page(f: TestHome, engines: Engines, lang: Lang = Lang.En, test: androidx.compose.ui.test.ComposeUiTest.() -> Unit) {
        val home = f.home()
        runComposeUiTest {
            setContent { InPlayer(home, lang) { Box(Modifier.size(900.dp, 1200.dp)) { PlaybackSettingsPage(home.prefs.playback, engineRow(home, engines)) } } }
            test()
        }
    }

    @Test
    fun automaticIsTheDefaultAndOlderCanBeChosen() = TestHome().use { f ->
        page(f, engines(loaded)) {
            onNodeWithTag("engine-Auto").performScrollTo().assertIsSelected().assertIsEnabled()
            onNodeWithTag("engine-Older").assertIsNotSelected().assertIsEnabled()
            onNodeWithText("Use the older engine only if videos do not play well. The home converts more files for it, so they take longer to start.").assertExists()
            onNodeWithTag("engine-locked").assertDoesNotExist()
            onNodeWithTag("engine-Older").performClick()
            onNodeWithTag("engine-Older").assertIsSelected()
            assertEquals(EngineChoice.Older, f.prefs.engine)
            assertEquals("javafx", f.prefs.store.get("player.engine"))
            onNodeWithTag("engine-Auto").performClick()
            assertEquals(EngineChoice.Auto, f.prefs.engine)
        }
    }

    @Test
    fun olderChosenShowsOlder() = TestHome().use { f ->
        f.prefs.chooseEngine(EngineChoice.Older)
        page(f, engines(loaded)) {
            onNodeWithTag("engine-Older").performScrollTo().assertIsSelected().assertIsEnabled()
            onNodeWithTag("engine-Auto").assertIsNotSelected()
        }
    }

    @Test
    fun theVariableLocksItOnTheOlderEngine() = TestHome().use { f ->
        page(f, engines(loaded, mapOf("MAKTABA_PLAYER_ENGINE" to "javafx"))) {
            onNodeWithTag("engine-Older").performScrollTo().assertIsSelected().assertIsNotEnabled()
            onNodeWithTag("engine-Auto").assertIsNotEnabled()
            onNodeWithText("Set by MAKTABA_PLAYER_ENGINE on this computer.").assertExists()
        }
    }

    @Test
    fun aMissingLibmpvLocksItWithTheReasonInArabicToo() = TestHome().use { f ->
        page(f, engines(missing)) {
            onNodeWithTag("engine-Older").performScrollTo().assertIsSelected().assertIsNotEnabled()
            onNodeWithText("The new engine could not start: libmpv-2.dll is not in C:\\Maktaba Player\\app.").assertExists()
            onNodeWithTag("engine-learn-more").performClick()
        }
        assertEquals(listOf("${f.home().webUrl}/help/desktop-player?lang=en#video-engine"), f.opened)
        f.opened.clear()
        f.prefs.chooseLang(Lang.Ar)
        page(f, engines(missing), Lang.Ar) {
            onNodeWithText("محرّك الفيديو").performScrollTo().assertExists()
            onNodeWithText("تعذّر تشغيل المحرّك الجديد: libmpv-2.dll is not in C:\\Maktaba Player\\app.").assertExists()
            onNodeWithTag("engine-learn-more").performClick()
        }
        // Learn more opens the help page in the Player's language
        assertEquals(listOf("${f.home().webUrl}/help/desktop-player?lang=ar#video-engine"), f.opened)
    }
}
