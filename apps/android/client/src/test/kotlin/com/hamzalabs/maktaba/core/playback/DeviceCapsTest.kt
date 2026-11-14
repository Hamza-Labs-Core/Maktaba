package com.hamzalabs.maktaba.core.playback

import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.jsonArray
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test
import java.io.File

/**
 * Story 58.05 T03 (58.02 T07): the report built from recorded `MediaCodecList`, display and
 * audio answers of real Fire TV (and Android TV) models is the one `shared/playback/caps-vectors.json`
 * says, the same file the home's decisions are tested against.
 */
class DeviceCapsTest {
    private val json = Json { ignoreUnknownKeys = true }

    private fun vectors(): File {
        var dir: File? = File("").absoluteFile
        while (dir != null) {
            val f = File(dir, "shared/playback/caps-vectors.json")
            if (f.isFile) return f
            dir = dir.parentFile
        }
        error("shared/playback/caps-vectors.json not found above ${File("").absolutePath}")
    }

    @Test
    fun everyAndroidVectorBuildsItsReport() {
        val all = Json.parseToJsonElement(vectors().readText()).jsonObject.getValue("vectors").jsonArray.map { it.jsonObject }
        val mine = all.filter { it.getValue("profile").jsonPrimitive.content in setOf("firetv", "androidtv") }
        assertTrue("three Fire TV models at least, found ${mine.map { it["id"] }}", mine.count { it.getValue("profile").jsonPrimitive.content == "firetv" } >= 3)
        for (v in mine) {
            val answers = json.decodeFromJsonElement(PlatformAnswers.serializer(), v.getValue("answers"))
            val want = v.getValue("device_caps") as JsonObject
            assertEquals("vector ${v["id"]}", want, DeviceCaps.build(answers).toJson())
        }
    }

    @Test
    fun decodersOfOneCodecMergeAndHdrNeedsTheDisplayAndADecoder() {
        val caps = DeviceCaps.build(
            PlatformAnswers(
                codecs = listOf(
                    CodecAnswer("video/hevc", maxHeight = 1080, maxFps = 60, profiles = listOf("Main")),
                    CodecAnswer("video/hevc", maxHeight = 2160, maxFps = 30, profiles = listOf("Main10")),
                    CodecAnswer("video/avc", maxHeight = 1080, maxFps = 60, profiles = listOf("High10")),
                    CodecAnswer("video/dolby-vision", maxHeight = 2160, profiles = listOf("DvheSt")),
                    CodecAnswer("audio/vnd.dts", null),
                    CodecAnswer("video/mpeg2", maxHeight = 1080),
                ),
                // a display with Dolby Vision but nothing else
                hdrTypes = listOf("DOLBY_VISION"),
                audio = AudioAnswer(listOf("PCM_16BIT", "DTS_HD", "E_AC3_JOC", "E_AC3"), 6),
            ),
        )
        assertEquals(
            listOf(VideoCap("h264", 1080, 60, 10), VideoCap("hevc", 2160, 60, 10)),
            caps.video,
        )
        assertEquals(listOf("dv-p8"), caps.hdr)
        assertEquals(listOf("dts"), caps.audio)
        assertEquals(listOf("eac3", "dts"), caps.passthrough)
        assertEquals(6, caps.maxAudioChannels)

        // HDR10 on the display with only 8-bit decoders is no HDR10
        val sdrDecoders = DeviceCaps.build(
            PlatformAnswers(codecs = listOf(CodecAnswer("video/hevc", maxHeight = 1080, profiles = listOf("Main"))), hdrTypes = listOf("HDR10", "HLG")),
        )
        assertEquals(emptyList<String>(), sdrDecoders.hdr)
        // and a DV decoder without a DV display is no Dolby Vision
        val noDvDisplay = DeviceCaps.build(
            PlatformAnswers(codecs = listOf(CodecAnswer("video/dolby-vision", profiles = listOf("DvheStn"))), hdrTypes = listOf("HDR10")),
        )
        assertEquals(emptyList<String>(), noDvDisplay.hdr)
    }

    @Test
    fun theJsonSendsEmptyListsAndLeavesOutWhatIsUnknown() {
        val j = DeviceCaps.build(PlatformAnswers()).toJson()
        assertEquals("[]", j.getValue("video").toString())
        assertEquals("[]", j.getValue("passthrough").toString())
        assertTrue("no channel count without an answer", "max_audio_channels" !in j)
        assertEquals("""["ts","fmp4"]""", j.getValue("hls_segments").toString())
    }
}
