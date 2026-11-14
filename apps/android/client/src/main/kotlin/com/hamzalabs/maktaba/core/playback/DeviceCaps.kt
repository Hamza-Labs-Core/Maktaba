package com.hamzalabs.maktaba.core.playback

import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.buildJsonArray
import kotlinx.serialization.json.buildJsonObject
import kotlinx.serialization.json.put

/**
 * What an Android TV or Fire TV says it can play (Epic 58 story 58.02's `device_caps`, built here
 * for story 58.05): read from the platform's own answers (`MediaCodecList`, the display's HDR
 * types, the audio output's encodings), so a 4K HDR stick gets 4K HDR and a Fire TV Stick Lite on
 * an SDR set gets what it really plays, model by model. The home narrows its profile (`firetv`,
 * `androidtv`) to it and widens it only up to the platform's ceiling.
 *
 * [PlatformAnswers] is the platform's answers as plain values (the TV app reads them on the
 * device, `DeviceCapsReader`); [build] turns them into the report. `shared/playback/caps-vectors.json`
 * holds recorded answers of real Fire TV models and the report each must give.
 */
@Serializable
data class PlatformAnswers(
    /** Each hardware decoder: its MIME type, the most it decodes, its profile names. */
    @SerialName("media_codec_list") val codecs: List<CodecAnswer> = emptyList(),
    /** The display's HDR types, by `Display.HdrCapabilities`' names (`HDR10`, `HLG`, `HDR10_PLUS`, `DOLBY_VISION`). */
    @SerialName("hdr_types") val hdrTypes: List<String> = emptyList(),
    /** What the audio output takes as it is (passthrough) and how many channels. */
    @SerialName("audio_capabilities") val audio: AudioAnswer = AudioAnswer(),
    /** The containers the player reads (Media3's extractors). */
    val containers: List<String> = DeviceCaps.MEDIA3_CONTAINERS,
)

/** One decoder. [profiles] are the `MediaCodecInfo.CodecProfileLevel` names without their codec prefix (`Main10HDR10`, `DvheSt`). */
@Serializable
data class CodecAnswer(
    val mime: String,
    @SerialName("max_width") val maxWidth: Int? = null,
    @SerialName("max_height") val maxHeight: Int? = null,
    @SerialName("max_fps") val maxFps: Int? = null,
    val profiles: List<String> = emptyList(),
)

/** The audio output: its encodings by `AudioFormat`'s names without `ENCODING_` (`AC3`, `E_AC3`), and its most channels. */
@Serializable
data class AudioAnswer(
    val encodings: List<String> = emptyList(),
    @SerialName("max_channels") val maxChannels: Int? = null,
)

/** One video codec of the report and its limits. */
data class VideoCap(val codec: String, val maxHeight: Int?, val maxFps: Int?, val bitDepth: Int?)

/** The report, as `POST /api/stream/sessions` takes it in `device_caps`. */
data class DeviceCaps(
    val containers: List<String>,
    val video: List<VideoCap>,
    val hdr: List<String>,
    val audio: List<String>,
    val passthrough: List<String>,
    val maxAudioChannels: Int?,
    val hlsSegments: List<String>,
    val dash: Boolean,
) {
    /** The `device_caps` JSON: every list sent, even empty (an empty list is the device's "none"). */
    fun toJson(): JsonObject = buildJsonObject {
        put("containers", strings(containers))
        put(
            "video",
            buildJsonArray {
                for (v in video) {
                    add(
                        buildJsonObject {
                            put("codec", v.codec)
                            v.maxHeight?.let { put("max_height", it) }
                            v.maxFps?.let { put("max_fps", it) }
                            v.bitDepth?.let { put("bit_depth", it) }
                        },
                    )
                }
            },
        )
        put("hdr", strings(hdr))
        put("audio", strings(audio))
        put("passthrough", strings(passthrough))
        maxAudioChannels?.let { put("max_audio_channels", it) }
        put("hls_segments", strings(hlsSegments))
        put("dash", dash)
    }

    private fun strings(l: List<String>) = buildJsonArray { l.forEach { add(kotlinx.serialization.json.JsonPrimitive(it)) } }

    companion object {
        /**
         * The report from the platform's [answers]. Decoders of one codec merge to the most any
         * of them does. A video codec's bit depth is 10 when a decoder lists a 10-bit profile, else
         * 8 (H.264 says none unless it has a 10-bit one: 8 is its every decoder). An HDR format
         * counts only when the display shows it and a decoder decodes it: HDR10 and HLG need a
         * 10-bit decoder, each Dolby Vision profile its own decoder profile.
         */
        fun build(answers: PlatformAnswers): DeviceCaps {
            data class Merged(var height: Int? = null, var fps: Int? = null, val profiles: MutableSet<String> = linkedSetOf())

            val videos = linkedMapOf<String, Merged>()
            val dvProfiles = linkedSetOf<String>()
            val audio = linkedSetOf<String>()
            for (c in answers.codecs) {
                val mime = c.mime.lowercase()
                if (mime == DV_MIME) {
                    c.profiles.mapNotNullTo(dvProfiles) { DV_PROFILES[it] }
                    continue
                }
                VIDEO_MIMES[mime]?.let { codec ->
                    val m = videos.getOrPut(codec) { Merged() }
                    m.height = maxOf(m.height, c.maxHeight)
                    m.fps = maxOf(m.fps, c.maxFps)
                    m.profiles += c.profiles
                }
                AUDIO_MIMES[mime]?.let { audio += it }
            }
            val video = VIDEO_ORDER.mapNotNull { codec ->
                val m = videos[codec] ?: return@mapNotNull null
                val tenBit = m.profiles.any { "10" in it || it.startsWith("Profile2") || it.startsWith("Profile3") }
                VideoCap(codec, m.height, m.fps, if (tenBit) 10 else if (codec == "h264") null else 8)
            }
            val decodes10Bit = video.any { (it.bitDepth ?: 8) >= 10 }
            val display = answers.hdrTypes.map { it.uppercase() }.toSet()
            val hdr = buildList {
                if (decodes10Bit && ("HDR10" in display || "HDR10_PLUS" in display)) add("hdr10")
                if (decodes10Bit && "HLG" in display) add("hlg")
                if ("DOLBY_VISION" in display) DV_ORDER.filterTo(this) { it in dvProfiles }
            }
            val passthrough = answers.audio.encodings.mapNotNull { PASSTHROUGH[it.uppercase()] }.toSet()
            return DeviceCaps(
                containers = answers.containers,
                video = video,
                hdr = hdr,
                audio = AUDIO_ORDER.filter { it in audio },
                passthrough = PASSTHROUGH_ORDER.filter { it in passthrough },
                maxAudioChannels = answers.audio.maxChannels,
                hlsSegments = MEDIA3_HLS_SEGMENTS,
                dash = true,
            )
        }

        private fun maxOf(a: Int?, b: Int?): Int? = if (a == null) b else if (b == null) a else kotlin.math.max(a, b)

        /** What Media3 reads in this app: MP4, Matroska and MPEG-TS files, HLS in TS or fMP4 segments, DASH. */
        val MEDIA3_CONTAINERS = listOf("mp4", "mkv", "ts")
        val MEDIA3_HLS_SEGMENTS = listOf("ts", "fmp4")

        private const val DV_MIME = "video/dolby-vision"
        private val VIDEO_MIMES = mapOf(
            "video/avc" to "h264",
            "video/hevc" to "hevc",
            "video/av01" to "av1",
            "video/x-vnd.on2.vp9" to "vp9",
        )
        private val VIDEO_ORDER = listOf("h264", "hevc", "av1", "vp9")

        /** Dolby Vision's profiles by `CodecProfileLevel` name: 5 (DvheStn), 7 (DvheDtb), 8 (DvheSt). */
        private val DV_PROFILES = mapOf("DvheStn" to "dv-p5", "DvheDtb" to "dv-p7", "DvheSt" to "dv-p8")
        private val DV_ORDER = listOf("dv-p5", "dv-p7", "dv-p8")

        private val AUDIO_MIMES = mapOf(
            "audio/mp4a-latm" to "aac",
            "audio/ac3" to "ac3",
            "audio/eac3" to "eac3",
            "audio/eac3-joc" to "eac3",
            "audio/ac4" to "ac4",
            "audio/opus" to "opus",
            "audio/flac" to "flac",
            "audio/mpeg" to "mp3",
            "audio/vorbis" to "vorbis",
            "audio/true-hd" to "truehd",
            "audio/vnd.dts" to "dts",
            "audio/vnd.dts.hd" to "dts",
        )
        private val AUDIO_ORDER = listOf("aac", "ac3", "eac3", "ac4", "opus", "flac", "mp3", "vorbis", "truehd", "dts")

        private val PASSTHROUGH = mapOf(
            "AC3" to "ac3",
            "E_AC3" to "eac3",
            "E_AC3_JOC" to "eac3",
            "AC4" to "ac4",
            "DOLBY_TRUEHD" to "truehd",
            "DTS" to "dts",
            "DTS_HD" to "dts",
        )
        private val PASSTHROUGH_ORDER = listOf("ac3", "eac3", "ac4", "truehd", "dts")
    }
}
