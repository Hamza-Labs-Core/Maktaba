package com.hamzalabs.maktaba.player.shell

import com.hamzalabs.maktaba.player.harness.Window
import com.hamzalabs.maktaba.player.harness.adaptiveInfo
import com.hamzalabs.maktaba.player.harness.info
import org.junit.Assert.assertEquals
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

    @Test
    fun anExpandedWindowHasTheDrawer() {
        assertEquals(ShellNavigation.Drawer, navigationFor(Window.Tablet.info()))
        assertEquals(ShellNavigation.Drawer, navigationFor(adaptiveInfo(840, 900)))
    }

    @Test
    fun tabletopHasTheBarAtAnyWidth() {
        assertEquals(ShellNavigation.Bar, navigationFor(Window.Foldable.info(tabletop = true)))
        assertEquals(ShellNavigation.Bar, navigationFor(Window.Tablet.info(tabletop = true)))
    }

    @Test
    fun aPhoneOnItsSideKeepsTheRail() {
        // wide but compact in height: a drawer's list would not fit
        assertEquals(ShellNavigation.Rail, navigationFor(adaptiveInfo(891, 411)))
    }
}
