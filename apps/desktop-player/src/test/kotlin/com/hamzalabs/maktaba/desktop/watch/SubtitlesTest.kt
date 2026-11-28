package com.hamzalabs.maktaba.desktop.watch

import com.hamzalabs.maktaba.core.api.VideoSubtitle
import com.hamzalabs.maktaba.core.playback.SubtitleColour
import com.hamzalabs.maktaba.core.playback.SubtitleSize
import com.hamzalabs.maktaba.desktop.Lang
import com.hamzalabs.maktaba.desktop.Texts
import com.hamzalabs.maktaba.desktop.mpv.MpvVideo
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

/** Story 13.20: direct play's Subtitles menu (T01), its names, and the OSD caption's size and colour (T02). */
class SubtitlesTest {
    /** A recorded track-list's subtitles: an Arabic SRT, an English ASS, a PGS and a forced SRT. */
    private val file = listOf(
        FileSubtitle("1", null, "ara", "subrip"),
        FileSubtitle("2", "English (styled)", "eng", "ass"),
        FileSubtitle("3", null, null, "hdmv_pgs_subtitle"),
        FileSubtitle("4", "Signs", "eng", "subrip", forced = true),
    )

    /** The home's list: a sidecar in English, its transcription in Arabic, and the file's Arabic track again. */
    private val home = listOf(
        VideoSubtitle(7, "en", "srt", "external"),
        VideoSubtitle(8, "ar", "vtt", "generated"),
        VideoSubtitle(9, "ar", "srt", "embedded"),
    )

    /** As Playback makes them: a file beside the video and the transcription, both at the session's sidecar path. */
    private fun url(s: VideoSubtitle) = "http://127.0.0.1:1/x/stream/s1/subs/sidecar/${s.language}.vtt"

    @Test
    fun t01_theHomesFirstThenTheFilesOwnWithoutEmbeddedOrForced() {
        val menu = directSubtitles(home, file, ::url)
        assertEquals(
            listOf(
                SubtitleOption.Kind.Home to "en", SubtitleOption.Kind.Home to "ar",
                SubtitleOption.Kind.File to "ara", SubtitleOption.Kind.File to "eng", SubtitleOption.Kind.File to null,
            ),
            menu.map { it.kind to it.language },
        )
        assertEquals("http://127.0.0.1:1/x/stream/s1/subs/sidecar/en.vtt", menu[0].url)
        assertEquals("http://127.0.0.1:1/x/stream/s1/subs/sidecar/ar.vtt", menu[1].url)
        assertEquals(listOf("external", "generated"), menu.take(2).map { it.source })
        assertEquals(listOf("1", "2", "3"), menu.drop(2).map { it.trackId })
        assertEquals(listOf(false, false, true), menu.drop(2).map { it.picture })

        val t = Texts(Lang.En)
        assertEquals(listOf(null, null, "In the file", "In the file", "In the file · Picture"), menu.map(t::subtitleNote))
        // the two Arabic rows (the home's transcription, the file's SRT) share a name: each gets its number
        assertEquals(listOf("English", "Arabic (2)", "Arabic (3)", "English (styled)", "Track 5"), t.subtitleNames(menu))
        assertEquals("في الملف · صورة", Texts(Lang.Ar).subtitleNote(menu[4]))
    }

    @Test
    fun rowsAtOneAddressAreOneEntryAndARowWithNoLanguageIsLeftOut() {
        val rows = listOf(
            VideoSubtitle(1, "ar", "srt", "external"),
            VideoSubtitle(2, "ar", "vtt", "external"),
            VideoSubtitle(3, "ar", "vtt", "generated"),
            VideoSubtitle(4, "en", "vtt", "generated"),
            VideoSubtitle(5, "", "srt", "external"),
            VideoSubtitle(6, "fr", "srt", "something-new"),
        )
        val menu = directSubtitles(rows, emptyList(), ::url)
        assertEquals(listOf("ar", "en"), menu.map { it.language })
        assertEquals(listOf("external", "generated"), menu.map { it.source })
    }

    @Test
    fun aHomeWithoutTheSessionsPathsLeavesItsRowsOut() {
        val menu = directSubtitles(home, file) { null }
        assertTrue(menu.all { it.kind == SubtitleOption.Kind.File })
        assertTrue(directSubtitles(emptyList(), emptyList()) { "x" }.isEmpty())
    }

    @Test
    fun aTrackWithNoTitleAndAnUndeterminedLanguageIsTrackN() {
        val menu = directSubtitles(emptyList(), listOf(FileSubtitle("1", null, "und", "subrip"), FileSubtitle("2", "  ", null, "ass"))) { null }
        assertEquals(listOf("Track 1", "Track 2"), Texts(Lang.En).subtitleNames(menu))
        assertEquals("المسار 1", Texts(Lang.Ar).subtitleNames(menu)[0])
    }

    @Test
    fun aFilesThreeLetterLanguagesAreNamed() {
        val t = Texts(Lang.En)
        assertEquals("French", t.trackName(null, "fra", 1))
        assertEquals("French", t.trackName(null, "fre", 1))
        assertEquals("German", t.trackName(null, "ger", 1))
        assertEquals("Arabic", t.trackName(null, "ara", 1))
        assertEquals("Arabic", t.trackName(null, "ar", 1))
        assertEquals("الفرنسية", Texts(Lang.Ar).trackName(null, "fra", 1))
        // a code no locale knows stays as it is
        assertEquals("xqz", t.trackName(null, "xqz", 1))
    }

    @Test
    fun anHlsRenditionIsOfferedAsAStreamSubtitle() {
        val r = Rendition("SUBTITLES", "s", "", "ar", false, "ar.m3u8")
        val o = SubtitleOption.of(r)
        assertEquals(SubtitleOption.Kind.Stream, o.kind)
        assertNull(o.name)
        assertEquals("Arabic", Texts(Lang.En).subtitleNames(listOf(o)).single())
    }

    @Test
    fun t02_theOsdCaptionTakesTheSizeAndColour() {
        val medium = MpvVideo.captionAss("نص", rtl = true, scale = SubtitleStyle().scale)
        val large = MpvVideo.captionAss("نص", rtl = true, scale = SubtitleStyle(SubtitleSize.Large, SubtitleColour.Yellow).scale, colour = SubtitleColour.Yellow)
        val fs = { s: String -> Regex("""\\fs(\d+)""").find(s)!!.groupValues[1].toInt() }
        assertEquals(56, fs(medium))
        assertEquals((MpvVideo.CAPTION_SIZE * 1.3f).toInt(), fs(large))
        assertTrue(large, large.contains("\\1c&H4DE1FF&"))
        assertTrue(medium, medium.contains("\\1c&HFFFFFF&"))
        // right to left: the embedding mark first
        assertTrue(large, large.contains("}\u202Bنص"))
        assertEquals("4DE1FF", MpvVideo.assColour("#FFE14D"))
    }
}
