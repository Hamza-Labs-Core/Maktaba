package com.hamzalabs.maktaba.desktop.shell

import com.hamzalabs.maktaba.core.api.Capabilities
import com.hamzalabs.maktaba.core.api.Feature
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

/** Story 55.01 T01–T02 and 55.02 T03–T04. */
class NavigatorTest {
    @Test
    fun eachSectionKeepsItsStack() {
        val nav = Navigator()
        assertEquals(Section.Home, nav.section)
        nav.push(Route.Video("v1"))
        nav.select(Section.Search)
        assertEquals(Route.Search, nav.current)
        nav.push(Route.Series("s1"))
        nav.select(Section.Home)
        assertEquals(Route.Video("v1"), nav.current)
        assertEquals(listOf(Route.Home, Route.Video("v1")), nav.stack(Section.Home))
        assertEquals(listOf(Route.Search, Route.Series("s1")), nav.stack(Section.Search))
        // chosen again while shown: back to its root
        nav.select(Section.Home)
        assertEquals(Route.Home, nav.current)
    }

    @Test
    fun theSamePageIsNeverOnTopTwiceAndBackStopsAtTheRoot() {
        val nav = Navigator()
        nav.push(Route.Video("v1"))
        nav.push(Route.Video("v1"))
        assertEquals(2, nav.stack(Section.Home).size)
        assertTrue(nav.canGoBack)
        assertTrue(nav.back())
        assertFalse(nav.back())
        assertEquals(Route.Home, nav.current)
    }

    @Test
    fun theFiveSectionsInTheAndroidOrder() {
        assertEquals(listOf("Home", "Libraries", "LiveTv", "Search", "Profile"), Section.shown(Capabilities.AllOn).map { it.name })
    }

    @Test
    fun liveTvOffIsHiddenFromAViewerAndShownToAnAdmin() {
        val viewer = Capabilities(off = setOf(Feature.LIVE_TV))
        val admin = Capabilities(off = setOf(Feature.LIVE_TV), admin = true)
        assertFalse(Section.LiveTv.shown(viewer))
        assertTrue(Section.LiveTv.shown(admin))
        assertEquals(4, Section.shown(viewer).size)
    }

    @Test
    fun aViewerOnLiveTvGoesHomeWhenItGoesOff() {
        val nav = Navigator()
        nav.select(Section.LiveTv)
        nav.push(Route.Channel("c1"))
        nav.follow(Capabilities.AllOn)
        assertEquals(Section.LiveTv, nav.section)
        nav.follow(Capabilities(off = setOf(Feature.LIVE_TV)))
        assertEquals(Section.Home, nav.section)
        assertEquals(listOf(Route.LiveTv), nav.stack(Section.LiveTv))
    }

    @Test
    fun ctrlFAsksForSearch() {
        val nav = Navigator()
        val before = nav.searchAsked
        nav.askSearch()
        assertEquals(Section.Search, nav.section)
        assertEquals(before + 1, nav.searchAsked)
    }
}
