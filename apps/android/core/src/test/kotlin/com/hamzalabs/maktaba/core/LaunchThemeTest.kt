package com.hamzalabs.maktaba.core

import androidx.compose.ui.graphics.Color
import com.hamzalabs.maktaba.core.ui.MaktabaTokens
import java.io.File
import javax.xml.parsers.DocumentBuilderFactory
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test
import org.w3c.dom.Element

/**
 * FR-3: the window before the first frame and the system's launch screen are ink with the logo,
 * in the system's light mode too (there is no white flash). Reads the core's `res/` as the
 * apps (Player, Remote: `android:theme="@style/Theme.Maktaba"`) get it.
 */
class LaunchThemeTest {
    private val res = File("src/main/res")

    private fun xml(path: String): Element =
        DocumentBuilderFactory.newInstance().newDocumentBuilder().parse(File(res, path)).documentElement

    private fun Element.children(tag: String): List<Element> {
        val nodes = getElementsByTagName(tag)
        return (0 until nodes.length).map { nodes.item(it) as Element }
    }

    /** The launch theme in [dir] (`values`, `values-v31`), its items by name. */
    private fun theme(dir: String): Pair<String, Map<String, String>> {
        val style = xml("$dir/themes.xml").children("style").single { it.getAttribute("name") == "Theme.Maktaba" }
        return style.getAttribute("parent") to style.children("item").associate { it.getAttribute("name") to it.textContent.trim() }
    }

    private fun color(name: String): Color {
        val hex = xml("values/themes.xml").children("color").single { it.getAttribute("name") == name }.textContent.trim()
        return Color(("FF" + hex.removePrefix("#")).toLong(16))
    }

    @Test
    fun theWindowIsInkInEveryMode() {
        assertEquals(MaktabaTokens.Dark.bg, color("maktaba_background"))
        assertEquals(MaktabaTokens.Dark.accent, color("maktaba_accent"))
        // one launch theme, not a light one for the system's light mode
        val variants = res.listFiles().orEmpty().filter { File(it, "themes.xml").exists() }.map { it.name }.sorted()
        assertEquals(listOf("values", "values-v31"), variants)
        assertFalse(variants.any { "night" in it })
        for (dir in variants) {
            val (parent, items) = theme(dir)
            assertFalse("$dir inherits a light theme: $parent", "Light" in parent)
            assertEquals("@color/maktaba_background", items["android:colorBackground"])
            assertEquals("@color/maktaba_background", items["android:statusBarColor"])
            assertEquals("@color/maktaba_background", items["android:navigationBarColor"])
        }
    }

    @Test
    fun beforeAndroid12TheWindowShowsTheLogo() {
        val (_, items) = theme("values")
        assertEquals("@drawable/maktaba_window", items["android:windowBackground"])
        val layers = xml("drawable/maktaba_window.xml").children("item")
        assertEquals("@color/maktaba_background", layers.first().getAttribute("android:drawable"))
        val logo = layers.last().children("bitmap").single()
        assertEquals("@mipmap/ic_launcher_foreground", logo.getAttribute("android:src"))
        assertEquals("center", logo.getAttribute("android:gravity"))
    }

    @Test
    fun theSystemLaunchScreenIsInkWithTheLogo() {
        val (_, items) = theme("values-v31")
        assertEquals("@color/maktaba_background", items["android:windowBackground"])
        assertEquals("@color/maktaba_background", items["android:windowSplashScreenBackground"])
        assertEquals("@mipmap/ic_launcher_foreground", items["android:windowSplashScreenAnimatedIcon"])
        assertEquals("@color/maktaba_background", items["android:windowSplashScreenIconBackgroundColor"])
        assertTrue(File(res, "mipmap-xxxhdpi/ic_launcher_foreground.png").exists())
    }
}
