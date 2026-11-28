package com.hamzalabs.maktaba.desktop.profile

import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.size
import androidx.compose.ui.Modifier
import androidx.compose.ui.test.ExperimentalTestApi
import androidx.compose.ui.test.assertIsNotSelected
import androidx.compose.ui.test.assertIsSelected
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import androidx.compose.ui.test.performScrollTo
import androidx.compose.ui.test.runComposeUiTest
import androidx.compose.ui.unit.dp
import com.hamzalabs.maktaba.core.playback.SubtitleColour
import com.hamzalabs.maktaba.core.playback.SubtitleSize
import com.hamzalabs.maktaba.desktop.InPlayer
import com.hamzalabs.maktaba.desktop.Lang
import com.hamzalabs.maktaba.desktop.TestHome
import org.junit.Assert.assertEquals
import org.junit.Test

/** Story 13.20: Profile → Playback's Subtitle size and Subtitle colour rows, in English and Arabic. */
@OptIn(ExperimentalTestApi::class)
class PlaybackSettingsPageTest {
    @Test
    fun theSizeAndColourAreChosenAndKept() = TestHome().use { f ->
        val home = f.home()
        runComposeUiTest {
            setContent { InPlayer(home) { Box(Modifier.size(900.dp, 1200.dp)) { PlaybackSettingsPage(home.prefs.playback) } } }
            onNodeWithTag("subtitle-size-Medium").performScrollTo().assertIsSelected()
            onNodeWithTag("subtitle-colour-White").assertIsSelected()
            onNodeWithText("Subtitle size").assertExists()
            onNodeWithText("Subtitle colour").assertExists()
            // picture subtitles are drawn as authored: the page says so under the colour row
            onNodeWithText("Subtitles with their own styling keep their colours. Picture subtitles, from Blu-ray and DVD files, are shown as they were made.").assertExists()
            onNodeWithTag("subtitle-size-Large").performClick()
            onNodeWithTag("subtitle-colour-Yellow").performClick()
            onNodeWithTag("subtitle-size-Large").assertIsSelected()
            onNodeWithTag("subtitle-colour-White").assertIsNotSelected()
        }
        assertEquals(SubtitleSize.Large, home.prefs.playback.current.subtitleSize)
        assertEquals(SubtitleColour.Yellow, home.prefs.playback.current.subtitleColour)
        assertEquals("yellow", f.prefs.store.get("playback.subtitle_colour"))
        assertEquals("l", f.prefs.store.get("playback.subtitle_size"))
    }

    @Test
    fun inArabic() = TestHome().use { f ->
        val home = f.home()
        runComposeUiTest {
            setContent { InPlayer(home, Lang.Ar) { Box(Modifier.size(900.dp, 1200.dp)) { PlaybackSettingsPage(home.prefs.playback) } } }
            for (s in listOf("حجم الترجمة", "صغير", "متوسط", "كبير", "لون الترجمة", "أبيض", "أصفر")) onNodeWithText(s).assertExists()
        }
    }
}
