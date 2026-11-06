package com.hamzalabs.maktaba.desktop.watch

import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Test

/** Story 55.08 T04: the right cue at each time. */
class VttTest {
    private val file = """
        WEBVTT
        X-TIMESTAMP-MAP=MPEGTS:900000,LOCAL:00:00:00.000

        1
        00:00:01.000 --> 00:00:04.000 align:center line:90%
        <v Shaykh>بسم الله</v> <b>الرحمن</b>

        00:00:03.500 --> 00:00:05.000
        &amp; overlap

        00:01:02.250 --> 00:01:03.000
        later
    """.trimIndent()

    @Test
    fun cuesAreReadWithoutTheirStyles() {
        val cues = Vtt.parse(file)
        assertEquals(3, cues.size)
        assertEquals(1_000L, cues[0].startMs)
        assertEquals("بسم الله الرحمن", cues[0].text)
        assertEquals("& overlap", cues[1].text)
        assertEquals(62_250L, cues[2].startMs)
    }

    @Test
    fun theTextAtEachTime() {
        val cues = Vtt.parse(file)
        assertNull(cues.at(500))
        assertEquals("بسم الله الرحمن", cues.at(2_000))
        assertEquals("بسم الله الرحمن\n& overlap", cues.at(3_700))
        assertEquals("& overlap", cues.at(4_500))
        assertNull(cues.at(5_000))
        assertEquals("later", cues.at(62_500))
    }

    @Test
    fun segmentedFilesLineUpByTheirTimestampMaps() {
        // MPEGTS 900000 is 10 s; the second segment's local times restart at 0, 60 s later
        val a = "WEBVTT\nX-TIMESTAMP-MAP=MPEGTS:900000,LOCAL:00:00:00.000\n\n00:00:01.000 --> 00:00:02.000\none\n"
        val b = "WEBVTT\nX-TIMESTAMP-MAP=MPEGTS:6300000,LOCAL:00:00:00.000\n\n00:00:01.000 --> 00:00:02.000\ntwo\n"
        val cues = Vtt.join(listOf(a, b))
        assertEquals(listOf(1_000L, 61_000L), cues.map { it.startMs })
        assertEquals(60_000L, Vtt.timestampShift(b) - Vtt.timestampShift(a))
        assertEquals(3_723_456L, Vtt.millis("01:02:03.456"))
        assertEquals(123_456L, Vtt.millis("02:03.456"))
    }
}
