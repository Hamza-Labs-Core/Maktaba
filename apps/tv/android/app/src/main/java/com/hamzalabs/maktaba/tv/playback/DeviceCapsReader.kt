package com.hamzalabs.maktaba.tv.playback

import android.content.Context
import android.hardware.display.DisplayManager
import android.media.MediaCodecInfo
import android.media.MediaCodecInfo.CodecProfileLevel as P
import android.media.MediaCodecList
import android.os.Build
import android.util.Log
import android.view.Display
import androidx.annotation.OptIn
import androidx.media3.common.AudioAttributes
import androidx.media3.common.C
import androidx.media3.common.util.UnstableApi
import androidx.media3.exoplayer.audio.AudioCapabilities
import com.hamzalabs.maktaba.core.playback.AudioAnswer
import com.hamzalabs.maktaba.core.playback.CodecAnswer
import com.hamzalabs.maktaba.core.playback.DeviceCaps
import com.hamzalabs.maktaba.core.playback.PlatformAnswers
import kotlinx.serialization.json.JsonObject

/**
 * Reads what this TV plays (story 58.05, for 58.02's `device_caps`), on a Fire TV and an Android
 * TV alike: the hardware video decoders and every audio decoder from `MediaCodecList`, the
 * display's HDR types, and what the audio output takes as it is (Media3's [AudioCapabilities],
 * which knows Fire OS's own surround setting). [DeviceCaps.build] turns them into the report.
 *
 * Read at each stream open, so a TV moved to another screen or receiver reports the new one.
 * Anything the platform will not answer is left out rather than guessed: a report that cannot be
 * read at all is null, and the home then uses the platform's profile alone.
 */
@OptIn(UnstableApi::class)
object DeviceCapsReader {
    private const val TAG = "DeviceCaps"

    /** The `device_caps` JSON, or null when the platform would not answer. */
    fun read(context: Context): JsonObject? = runCatching { DeviceCaps.build(answers(context)).toJson() }
        .onFailure { Log.w(TAG, "could not read this TV's playback capabilities", it) }
        .getOrNull()

    fun answers(context: Context): PlatformAnswers = PlatformAnswers(
        codecs = codecs(),
        hdrTypes = runCatching { hdrTypes(context) }.getOrDefault(emptyList()),
        audio = runCatching { audio(context) }.getOrDefault(AudioAnswer()),
    )

    private fun codecs(): List<CodecAnswer> {
        val out = mutableListOf<CodecAnswer>()
        for (info in MediaCodecList(MediaCodecList.REGULAR_CODECS).codecInfos) {
            if (info.isEncoder) continue
            for (type in info.supportedTypes) {
                val mime = type.lowercase()
                val caps = runCatching { info.getCapabilitiesForType(type) }.getOrNull() ?: continue
                if (mime.startsWith("audio/")) {
                    out += CodecAnswer(mime)
                    continue
                }
                // a software video decoder claims sizes the TV cannot play smoothly: hardware only
                if (!mime.startsWith("video/") || softwareOnly(info)) continue
                val video = caps.videoCapabilities
                val size = video?.let { v -> SIZES.firstOrNull { (w, h) -> runCatching { v.isSizeSupported(w, h) }.getOrDefault(false) } }
                val fps = if (video != null && size != null) {
                    runCatching { video.getSupportedFrameRatesFor(size.first, size.second).upper.toInt() }.getOrNull()
                } else {
                    null
                }
                out += CodecAnswer(
                    mime,
                    maxWidth = size?.first,
                    maxHeight = size?.second,
                    maxFps = fps,
                    profiles = caps.profileLevels.mapNotNull { profileName(mime, it.profile) }.distinct(),
                )
            }
        }
        return out
    }

    private fun softwareOnly(info: MediaCodecInfo): Boolean =
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.Q) {
            info.isSoftwareOnly
        } else {
            // Fire OS 7 (API 28): Android's own decoders by name
            val n = info.name.lowercase()
            n.startsWith("omx.google.") || n.startsWith("c2.android.")
        }

    /** The display's HDR types by name; Android 14 moved them to the display mode. */
    @Suppress("DEPRECATION")
    private fun hdrTypes(context: Context): List<String> {
        val display = context.getSystemService(DisplayManager::class.java)?.getDisplay(Display.DEFAULT_DISPLAY) ?: return emptyList()
        val types = if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.UPSIDE_DOWN_CAKE) {
            display.mode.supportedHdrTypes
        } else {
            display.hdrCapabilities?.supportedHdrTypes ?: IntArray(0)
        }
        return types.toList().mapNotNull {
            when (it) {
                Display.HdrCapabilities.HDR_TYPE_DOLBY_VISION -> "DOLBY_VISION"
                Display.HdrCapabilities.HDR_TYPE_HDR10 -> "HDR10"
                Display.HdrCapabilities.HDR_TYPE_HLG -> "HLG"
                Display.HdrCapabilities.HDR_TYPE_HDR10_PLUS -> "HDR10_PLUS"
                else -> null
            }
        }.distinct()
    }

    private fun audio(context: Context): AudioAnswer {
        val caps = AudioCapabilities.getCapabilities(context, AudioAttributes.DEFAULT, null)
        val encodings = buildList {
            add("PCM_16BIT")
            for ((encoding, name) in ENCODINGS) if (caps.supportsEncoding(encoding)) add(name)
        }
        return AudioAnswer(encodings, caps.maxChannelCount)
    }

    /** The 16:9 sizes asked for, largest first. */
    private val SIZES = listOf(3840 to 2160, 2560 to 1440, 1920 to 1080, 1280 to 720, 720 to 480)

    private val ENCODINGS = listOf(
        C.ENCODING_AC3 to "AC3",
        C.ENCODING_E_AC3 to "E_AC3",
        C.ENCODING_E_AC3_JOC to "E_AC3_JOC",
        C.ENCODING_AC4 to "AC4",
        C.ENCODING_DOLBY_TRUEHD to "DOLBY_TRUEHD",
        C.ENCODING_DTS to "DTS",
        C.ENCODING_DTS_HD to "DTS_HD",
    )

    /** A `CodecProfileLevel` profile's name without its codec prefix, as `PlatformAnswers` takes it. */
    internal fun profileName(mime: String, profile: Int): String? = when (mime) {
        "video/avc" -> AVC[profile]
        "video/hevc" -> HEVC[profile]
        "video/av01" -> AV1[profile]
        "video/x-vnd.on2.vp9" -> VP9[profile]
        "video/dolby-vision" -> DV[profile]
        else -> null
    }

    private val AVC = mapOf(
        P.AVCProfileBaseline to "Baseline", P.AVCProfileConstrainedBaseline to "ConstrainedBaseline",
        P.AVCProfileMain to "Main", P.AVCProfileExtended to "Extended", P.AVCProfileHigh to "High",
        P.AVCProfileConstrainedHigh to "ConstrainedHigh", P.AVCProfileHigh10 to "High10",
        P.AVCProfileHigh422 to "High422", P.AVCProfileHigh444 to "High444",
    )
    private val HEVC = mapOf(
        P.HEVCProfileMain to "Main", P.HEVCProfileMain10 to "Main10", P.HEVCProfileMainStill to "MainStill",
        P.HEVCProfileMain10HDR10 to "Main10HDR10", P.HEVCProfileMain10HDR10Plus to "Main10HDR10Plus",
    )
    private val AV1 = mapOf(
        P.AV1ProfileMain8 to "Main8", P.AV1ProfileMain10 to "Main10",
        P.AV1ProfileMain10HDR10 to "Main10HDR10", P.AV1ProfileMain10HDR10Plus to "Main10HDR10Plus",
    )
    private val VP9 = mapOf(
        P.VP9Profile0 to "Profile0", P.VP9Profile1 to "Profile1", P.VP9Profile2 to "Profile2", P.VP9Profile3 to "Profile3",
        P.VP9Profile2HDR to "Profile2HDR", P.VP9Profile3HDR to "Profile3HDR",
        P.VP9Profile2HDR10Plus to "Profile2HDR10Plus", P.VP9Profile3HDR10Plus to "Profile3HDR10Plus",
    )
    private val DV = mapOf(
        P.DolbyVisionProfileDvavPer to "DvavPer", P.DolbyVisionProfileDvavPen to "DvavPen",
        P.DolbyVisionProfileDvheDer to "DvheDer", P.DolbyVisionProfileDvheDen to "DvheDen",
        P.DolbyVisionProfileDvheDtr to "DvheDtr", P.DolbyVisionProfileDvheStn to "DvheStn",
        P.DolbyVisionProfileDvheDth to "DvheDth", P.DolbyVisionProfileDvheDtb to "DvheDtb",
        P.DolbyVisionProfileDvheSt to "DvheSt", P.DolbyVisionProfileDvavSe to "DvavSe",
    )
}
