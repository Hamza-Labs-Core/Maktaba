package com.hamzalabs.maktaba.desktop.mpv

import com.hamzalabs.maktaba.core.playback.DeviceCaps
import com.hamzalabs.maktaba.core.playback.VideoCap

/**
 * The desktop Player's `device_caps` (story 13.19): what the [Probe] measured on this machine,
 * as the shared client's [DeviceCaps] (the Android report's class and JSON).
 *
 * - `video`: one entry per codec that decoded, in the profile's order: its height (2160 when a
 *   hardware decoder took every clip of it, else the height its slowest clip decodes smoothly
 *   in software, [heightFor]), its bit depth (10 when its 10-bit clip decoded, 8 when only the
 *   8-bit one did; not said for H.264 and MPEG-2, which have no 10-bit clip), and `hardware`.
 *   A codec none of whose clips decoded is left out (the home transcodes it).
 * - `max_audio_channels`: the audio device's channels, at most the profile's 8; left out when
 *   the device could not be opened.
 * - containers, audio, HDR (and the TV fields) are left out: the desktop-mpv profile's lists
 *   stand (mpv reads every container and decodes every audio codec of the profile, and
 *   tone-maps HDR on any display).
 */
object DesktopCaps {
    /** The desktop-mpv profile's video codecs, in its order (streaming/internal/capability/matrix.go). */
    val CODECS = listOf("h264", "hevc", "vp9", "av1", "mpeg2video")

    /** The profile's most channels. */
    const val MAX_CHANNELS = 8

    /** Frames per second a picture must decode at to play smoothly: 1.5 times 30. */
    const val SMOOTH_FPS = 45.0

    /**
     * The tallest picture software decodes smoothly, from its frames per second at 2160p,
     * scaled by pixel count: 2160 at 45 fps or more, 1440 at 45 scaled by 9/16, 1080 at 45
     * scaled by 1/4, else 720.
     */
    fun heightFor(fpsAt2160: Double): Int = when {
        fpsAt2160 >= SMOOTH_FPS -> 2160
        fpsAt2160 >= SMOOTH_FPS * 9 / 16 -> 1440
        fpsAt2160 >= SMOOTH_FPS / 4 -> 1080
        else -> 720
    }

    /** The report from the probe's [clips] and the audio device's [channels] (null: unknown). */
    fun build(clips: List<ClipResult>, channels: Int?): DeviceCaps {
        val video = CODECS.mapNotNull { codec ->
            val decoded = clips.filter { it.codec == codec && it.decoded }
            if (decoded.isEmpty()) return@mapNotNull null
            val hardware = decoded.all { it.hardware }
            val height = decoded.minOf { if (it.hardware) 2160 else heightFor(it.softwareFps ?: 0.0) }
            val hasTenBitClip = clips.any { it.codec == codec && it.bitDepth >= 10 }
            val depth = when {
                decoded.any { it.bitDepth >= 10 } -> 10
                hasTenBitClip -> 8
                else -> null
            }
            VideoCap(codec, height, null, depth, hardware)
        }
        return DeviceCaps(
            containers = null,
            video = video,
            hdr = null,
            audio = null,
            passthrough = null,
            maxAudioChannels = channels?.takeIf { it > 0 }?.coerceAtMost(MAX_CHANNELS),
            hlsSegments = null,
            dash = null,
        )
    }
}
