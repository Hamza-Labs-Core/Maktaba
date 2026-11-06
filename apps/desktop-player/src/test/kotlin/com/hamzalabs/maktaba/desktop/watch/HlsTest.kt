package com.hamzalabs.maktaba.desktop.watch

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

/** Story 55.08 T03: the master playlist's renditions, and the audio JavaFX is given. */
class HlsTest {
    private val master = """
        #EXTM3U
        #EXT-X-MEDIA:TYPE=AUDIO,GROUP-ID="aud",NAME="العربية",LANGUAGE="ar",DEFAULT=YES,AUTOSELECT=YES,URI="audio/ar.m3u8"
        #EXT-X-MEDIA:TYPE=AUDIO,GROUP-ID="aud",NAME="English",LANGUAGE="en",DEFAULT=NO,AUTOSELECT=NO,URI="audio/en.m3u8"
        #EXT-X-MEDIA:TYPE=SUBTITLES,GROUP-ID="subs",NAME="Arabic",LANGUAGE="ar",DEFAULT=NO,URI="subs/ar.m3u8"
        #EXT-X-STREAM-INF:BANDWIDTH=4000000,RESOLUTION=1280x720,AUDIO="aud",SUBTITLES="subs"
        720p.m3u8
    """.trimIndent()

    @Test
    fun theRenditionsAreRead() {
        assertTrue(isMaster(master))
        assertFalse(isMaster("#EXTM3U\n#EXTINF:6,\nseg0.ts"))
        val m = parseMaster(master)
        assertEquals(listOf("ar", "en"), m.audio.map { it.language })
        assertTrue(m.audio[0].default)
        assertEquals("English", m.audio[1].name)
        assertEquals("subs/ar.m3u8", m.subtitles.single().uri)
    }

    @Test
    fun theChosenAudioIsTheOnlyOneAndTheDefault() {
        val out = rewriteMaster(master, "English")
        val audio = out.lines().filter { it.startsWith("#EXT-X-MEDIA:TYPE=AUDIO") }
        assertEquals(1, audio.size)
        assertTrue(audio[0], audio[0].contains("LANGUAGE=\"en\"") && audio[0].contains("DEFAULT=YES") && audio[0].contains("AUTOSELECT=YES"))
        // JavaFX gets no subtitles: the Player draws them
        assertFalse(out.contains("TYPE=SUBTITLES"))
        assertFalse(out.contains("SUBTITLES=\"subs\""))
        assertTrue(out.contains("720p.m3u8"))
    }

    @Test
    fun withNoChoiceTheAudioIsAsTheHomeWroteIt() {
        val out = rewriteMaster(master, null)
        assertEquals(2, out.lines().count { it.startsWith("#EXT-X-MEDIA:TYPE=AUDIO") })
        assertEquals(2, rewriteMaster(master, "Klingon").lines().count { it.startsWith("#EXT-X-MEDIA:TYPE=AUDIO") })
    }

    @Test
    fun aStreamWithOneMuxedTrackHasNoAudioRenditions() {
        val m = parseMaster("#EXTM3U\n#EXT-X-STREAM-INF:BANDWIDTH=1\nv.m3u8")
        assertTrue(m.audio.isEmpty())
        assertEquals(listOf("a.vtt", "b.vtt"), segmentUris("#EXTM3U\n#EXTINF:60,\na.vtt\n\n#EXTINF:60,\nb.vtt\n#EXT-X-ENDLIST"))
    }
}
