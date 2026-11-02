package com.hamzalabs.maktaba.player.shell

import androidx.compose.material3.adaptive.HingeInfo
import androidx.compose.material3.adaptive.Posture
import androidx.compose.material3.adaptive.WindowAdaptiveInfo
import androidx.compose.ui.geometry.Rect
import com.hamzalabs.maktaba.player.harness.Window
import com.hamzalabs.maktaba.player.harness.adaptiveInfo
import com.hamzalabs.maktaba.player.harness.uprightHinge
import com.hamzalabs.maktaba.player.harness.folded
import com.hamzalabs.maktaba.player.harness.info
import com.hamzalabs.maktaba.player.harness.acrossHinge
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Test

/** FR-8: which navigation each window gets, from its size class and posture. */
class NavigationForTest {
    @Test
    fun aCompactWindowHasTheBar() {
        assertEquals(ShellNavigation.Bar, navigationFor(Window.Phone.info()))
        assertEquals(ShellNavigation.Bar, navigationFor(adaptiveInfo(599, 900)))
    }

    @Test
    fun aMediumWindowHasTheRail() {
        assertEquals(ShellNavigation.Rail, navigationFor(Window.Foldable.info()))
        assertEquals(ShellNavigation.Rail, navigationFor(adaptiveInfo(600, 900)))
        // a small tablet in portrait
        assertEquals(ShellNavigation.Rail, navigationFor(adaptiveInfo(800, 1280)))
    }

    /** No permanent drawer any more: it took 360 dp and squeezed a list-detail page. */
    @Test
    fun anExpandedWindowHasTheCollapsibleRail() {
        assertEquals(ShellNavigation.CollapsibleRail, navigationFor(Window.Tablet.info()))
        assertEquals(ShellNavigation.CollapsibleRail, navigationFor(Window.FoldableWide.info()))
        assertEquals(ShellNavigation.CollapsibleRail, navigationFor(adaptiveInfo(840, 900)))
    }

    @Test
    fun tabletopHasTheBarAtAnyWidth() {
        assertEquals(ShellNavigation.Bar, navigationFor(Window.Foldable.info(tabletop = true)))
        assertEquals(ShellNavigation.Bar, navigationFor(Window.Tablet.info(tabletop = true)))
        assertEquals(ShellNavigation.Bar, navigationFor(Window.Foldable.folded(acrossHinge(1346, 1682), vertical = false)))
    }

    @Test
    fun halfFoldedLikeABookHasTheCollapsibleRailAtAnyWidth() {
        assertEquals(ShellNavigation.CollapsibleRail, navigationFor(Window.FoldableWide.folded(uprightHinge(1682, 1346), vertical = true)))
        // medium wide: without the hinge, the plain rail
        assertEquals(ShellNavigation.CollapsibleRail, navigationFor(Window.Foldable.folded(uprightHinge(1346, 1682), vertical = true)))
    }

    @Test
    fun aFoldOpenFlatIsNoBook() {
        val flat = WindowAdaptiveInfo(
            Window.Foldable.info().windowSizeClass,
            Posture(hingeList = listOf(HingeInfo(Rect(670f, 0f, 676f, 1682f), isFlat = true, isVertical = true, isSeparating = false, isOccluding = false))),
        )
        assertNull(bookHinge(flat))
        assertEquals(ShellNavigation.Rail, navigationFor(flat))
    }

    @Test
    fun aPhoneOnItsSideKeepsTheRail() {
        // wide but compact in height: the collapsible rail's list would not fit
        assertEquals(ShellNavigation.Rail, navigationFor(adaptiveInfo(891, 411)))
    }
}
