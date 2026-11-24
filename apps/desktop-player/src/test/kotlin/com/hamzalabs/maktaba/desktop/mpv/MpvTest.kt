package com.hamzalabs.maktaba.desktop.mpv

import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.rules.TemporaryFolder

/** Story 13.18: where libmpv is looked for, why it is not used (T06), and the captions' ASS. */
class MpvTest {
    @get:Rule val tmp = TemporaryFolder()

    private fun reason(l: MpvLoad) = (l as MpvLoad.Missing).reason

    @Test
    fun theLibraryIsLookedForInTheInstallFolderOnly() {
        assertEquals("libmpv-2.dll", MpvLoad.fileName("Windows 11"))
        assertEquals("libmpv.so.2", MpvLoad.fileName("Linux"))
        // a development run has no app resources: JavaFX, and the reason says so
        assertTrue(reason(MpvLoad.from(null, emptyMap())).contains("development"))
        // an install folder without it (a broken install): the file and the folder are named
        val missing = reason(MpvLoad.from(tmp.root.absolutePath, emptyMap()))
        assertTrue(missing, missing.contains(MpvLoad.fileName()) && missing.contains(tmp.root.absolutePath))
    }

    @Test
    fun aFileThatIsNotALibraryIsReportedNotLoaded() {
        tmp.newFile(MpvLoad.fileName()).writeText("not a library")
        val why = reason(MpvLoad.from(tmp.root.absolutePath, emptyMap()))
        assertTrue(why, why.contains("did not load"))
    }

    @Test
    fun theSupportPagesSettingPlaysWithJavaFx() {
        tmp.newFile(MpvLoad.fileName()).writeText("whatever")
        assertEquals("MAKTABA_PLAYER_ENGINE=javafx", reason(MpvLoad.from(tmp.root.absolutePath, mapOf("MAKTABA_PLAYER_ENGINE" to "JavaFX"))))
    }

    @Test
    fun aCaptionIsOneAssEventWithItsOwnCharactersEscaped() {
        val ass = MpvVideo.captionAss("a {b} \\c\nd", rtl = false, scale = 1f)
        assertTrue(ass, ass.startsWith("{\\an2\\fs32\\"))
        assertTrue(ass, ass.endsWith("a \\{b} \\\u2060c\\Nd"))
        // right to left: an embedding mark first, so "13 دقيقة" reads from the right
        assertTrue(MpvVideo.captionAss("13 دقيقة", rtl = true, scale = 1f).contains("}\u202B13 دقيقة"))
        // the Player's caption size setting scales it, within bounds
        assertTrue(MpvVideo.captionAss("x", rtl = false, scale = 2f).contains("\\fs65"))
        assertTrue(MpvVideo.captionAss("x", rtl = false, scale = 10f).contains("\\fs96"))
    }
}
