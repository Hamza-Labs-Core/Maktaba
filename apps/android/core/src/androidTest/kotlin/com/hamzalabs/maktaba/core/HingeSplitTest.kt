package com.hamzalabs.maktaba.core

import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.BoxWithConstraints
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.material3.Text
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalLayoutDirection
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.test.getBoundsInRoot
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.LayoutDirection
import androidx.compose.ui.unit.dp
import androidx.test.ext.junit.runners.AndroidJUnit4
import com.hamzalabs.maktaba.core.layout.HingeSplit
import com.hamzalabs.maktaba.core.layout.Panes
import com.hamzalabs.maktaba.core.ui.MaktabaTheme
import com.hamzalabs.maktaba.core.ui.ThemeMode
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith

/**
 * A book posture's split (story 42.8) with the hinge off centre, at a third of the window:
 * the hinge's pixels stay empty, and right to left the first pane is the right-hand part.
 */
@RunWith(AndroidJUnit4::class)
class HingeSplitTest {
    @get:Rule
    val rule = createComposeRule()

    private var widthDp = 0.dp
    private var hingeStartDp = 0.dp
    private var hingeEndDp = 0.dp

    private fun show(rtl: Boolean) {
        rule.setContent {
            CompositionLocalProvider(LocalLayoutDirection provides if (rtl) LayoutDirection.Rtl else LayoutDirection.Ltr) {
                MaktabaTheme(ThemeMode.Dark) {
                    BoxWithConstraints(Modifier.fillMaxSize()) {
                        val px = constraints.maxWidth
                        val hinge = (px / 3)..(px / 3 + px / 50)
                        widthDp = maxWidth
                        hingeStartDp = maxWidth * (hinge.first.toFloat() / px)
                        hingeEndDp = maxWidth * (hinge.last.toFloat() / px)
                        HingeSplit(
                            Panes.Book(hinge),
                            first = { Box(Modifier.fillMaxSize().testTag("first")) { Text("first") } },
                            second = { Box(Modifier.fillMaxSize().testTag("second")) { Text("second") } },
                        )
                    }
                }
            }
        }
    }

    private fun near(expected: Dp, actual: Dp, what: String) =
        assertEquals(what, expected.value, actual.value, 2f)

    @Test
    fun leftToRightTheFirstPaneIsLeftOfTheHinge() {
        show(rtl = false)
        val first = rule.onNodeWithTag("first").getBoundsInRoot()
        val second = rule.onNodeWithTag("second").getBoundsInRoot()
        near(0.dp, first.left, "first starts at the left")
        near(hingeStartDp, first.right, "first ends at the hinge")
        near(hingeEndDp, second.left, "second starts after the hinge")
        near(widthDp, second.right, "second ends at the right")
    }

    @Test
    fun rightToLeftTheFirstPaneIsRightOfTheHinge() {
        show(rtl = true)
        val first = rule.onNodeWithTag("first").getBoundsInRoot()
        val second = rule.onNodeWithTag("second").getBoundsInRoot()
        near(hingeEndDp, first.left, "first starts after the hinge")
        near(widthDp, first.right, "first ends at the right")
        near(0.dp, second.left, "second starts at the left")
        near(hingeStartDp, second.right, "second ends at the hinge")
        assertTrue("first leads, on the right", first.left > second.right)
    }
}
