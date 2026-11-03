package com.hamzalabs.maktaba.remote

import androidx.activity.ComponentActivity
import androidx.compose.ui.semantics.SemanticsActions
import androidx.compose.ui.semantics.SemanticsNode
import androidx.compose.ui.semantics.SemanticsProperties
import androidx.compose.ui.semantics.getOrNull
import androidx.compose.ui.test.hasTestTag
import androidx.compose.ui.test.junit4.v2.createAndroidComposeRule
import androidx.compose.ui.test.onRoot
import androidx.compose.ui.test.performClick
import androidx.compose.ui.text.TextLayoutResult
import androidx.test.ext.junit.runners.AndroidJUnit4
import com.github.takahirom.roborazzi.RoborazziOptions
import com.github.takahirom.roborazzi.captureRoboImage
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import org.robolectric.annotation.GraphicsMode
import com.hamzalabs.maktaba.core.R as CoreR

/**
 * "Approve a TV" in sight on every Remote place's top bar (the owner saw a TV's sign-in code
 * and could not find where to approve it: it was only in the account menu). The button is on
 * the bar before the account menu, inside the screen, says what it does, and opens "Approve a
 * sign-in". On the cover screens at a large text size the title ends in "…" but stays.
 */
@RunWith(AndroidJUnit4::class)
@GraphicsMode(GraphicsMode.Mode.NATIVE)
class ApproveTvTest {
    @get:Rule(order = 0) val home = FakeRemoteHome()
    @get:Rule(order = 1) val compose = createAndroidComposeRule<ComponentActivity>()

    private fun node(tag: String): SemanticsNode =
        compose.onAllNodes(hasTestTag(tag), useUnmergedTree = true).fetchSemanticsNodes().first()

    /** The bar fits: the button whole inside the screen, before the account menu, and a title. */
    private fun assertBarFits() {
        val width = compose.onRoot().fetchSemanticsNode().size.width.toFloat()
        val button = compose.onAllNodes(hasTestTag("approve-tv")).fetchSemanticsNodes().single()
        val b = button.boundsInRoot
        assertTrue("the button inside the screen: $b in 0..$width", b.left >= 0f && b.right <= width && b.width >= 40 * compose.density.density)
        assertEquals(listOf(compose.activity.getString(CoreR.string.approve_tv)), button.config.getOrNull(SemanticsProperties.ContentDescription))
        val account = compose.onAllNodes(androidx.compose.ui.test.hasContentDescription(compose.activity.getString(CoreR.string.account)))
            .fetchSemanticsNodes().single().boundsInRoot
        val rtl = compose.activity.resources.configuration.layoutDirection == android.view.View.LAYOUT_DIRECTION_RTL
        assertTrue("before the account menu: $b, account $account", if (rtl) b.left >= account.right - 1f else b.right <= account.left + 1f)
        val title = node("bar-title")
        assertTrue("a title is shown: ${title.boundsInRoot}", title.boundsInRoot.width >= 24 * compose.density.density)
        val layout = mutableListOf<TextLayoutResult>()
        title.config[SemanticsActions.GetTextLayoutResult].action!!.invoke(layout)
        assertEquals("the title on one line", 1, layout.single().lineCount)
    }

    @Test @Config(qualifiers = "w393dp-h916dp-440dpi")
    fun everyPlaceHasTheButtonAndItOpensApprove() {
        compose.setContent { RemoteForTest(home, RemoteWindow.RazrFoldCover.info) }
        Place.entries.forEach { place ->
            compose.onNode(hasTestTag("place-${place.name}")).performClick()
            compose.settledUntil { compose.has("approve-tv") }
            assertBarFits()
            compose.onNode(hasTestTag("approve-tv")).performClick()
            compose.settledUntil { compose.has("approve-code") }
            assertTrue(
                "${place.name}: the dialog says Approve a sign-in",
                compose.onAllNodes(androidx.compose.ui.test.hasText(compose.activity.getString(CoreR.string.approve_title)), useUnmergedTree = true)
                    .fetchSemanticsNodes().isNotEmpty(),
            )
            androidx.test.espresso.Espresso.pressBack()
            compose.settledUntil { !compose.has("approve-code") }
        }
    }

    private fun shoot(window: RemoteWindow, name: String, fontScale: Float = 1f) {
        compose.setContent { RemoteForTest(home, window.info, fontScale) }
        compose.settledUntil { compose.has("group-fingerprint:nas_partial_read") }
        compose.waitForIdle()
        assertBarFits()
        compose.onRoot().captureRoboImage("src/test/screenshots/$name.png", roborazziOptions = options)
    }

    @Test @Config(qualifiers = "w393dp-h916dp-440dpi")
    fun razrEnglish() = shoot(RemoteWindow.RazrFoldCover, "approvetv_razrfoldcover_dark_en")

    @Test @Config(qualifiers = "w393dp-h916dp-440dpi")
    fun razrEnglishLargestText() = shoot(RemoteWindow.RazrFoldCover, "approvetv_razrfoldcover_dark_en_font200", 2f)

    @Test @Config(qualifiers = "ar-w393dp-h916dp-440dpi")
    fun razrArabic() = shoot(RemoteWindow.RazrFoldCover, "approvetv_razrfoldcover_dark_ar")

    @Test @Config(qualifiers = "ar-w393dp-h916dp-440dpi")
    fun razrArabicLargestText() = shoot(RemoteWindow.RazrFoldCover, "approvetv_razrfoldcover_dark_ar_font200", 2f)

    @Test @Config(qualifiers = "w344dp-h882dp-420dpi")
    fun foldEnglish() = shoot(RemoteWindow.FoldCover, "approvetv_foldcover_dark_en")

    @Test @Config(qualifiers = "w344dp-h882dp-420dpi")
    fun foldEnglishLargestText() = shoot(RemoteWindow.FoldCover, "approvetv_foldcover_dark_en_font200", 2f)

    @Test @Config(qualifiers = "ar-w344dp-h882dp-420dpi")
    fun foldArabic() = shoot(RemoteWindow.FoldCover, "approvetv_foldcover_dark_ar")

    @Test @Config(qualifiers = "ar-w344dp-h882dp-420dpi")
    fun foldArabicLargestText() = shoot(RemoteWindow.FoldCover, "approvetv_foldcover_dark_ar_font200", 2f)

    private companion object {
        val options = RoborazziOptions(compareOptions = RoborazziOptions.CompareOptions(changeThreshold = 0.02f))
    }
}
