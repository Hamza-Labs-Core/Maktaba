package com.hamzalabs.maktaba.player.watch

import com.hamzalabs.maktaba.player.profile.PlaybackValues
import androidx.annotation.OptIn
import androidx.media3.common.C
import androidx.media3.common.Player
import androidx.media3.common.TrackSelectionOverride
import androidx.media3.common.TrackSelectionParameters
import androidx.media3.common.Tracks
import androidx.media3.common.util.UnstableApi
import com.hamzalabs.maktaba.core.api.Rendition
import java.util.Locale

/**
 * A subtitle or an audio track the picker offers (FR-34): the session's subtitle renditions
 * (HLS `EXT-X-MEDIA TYPE=SUBTITLES` since 48.4) and the stream's audio tracks, as the player
 * reads them.
 */
data class TrackOption(
    /** [C.TRACK_TYPE_TEXT] or [C.TRACK_TYPE_AUDIO]. */
    val type: Int,
    /** Where it is in [Tracks.getGroups]. */
    val group: Int,
    val track: Int,
    /** The stream's own name for it (an HLS rendition's NAME), if any. */
    val label: String?,
    /** Its language (normalized, like "en"), if known. */
    val language: String?,
    val selected: Boolean,
) {
    /**
     * What a person reads: its name, else its language in [locale]'s words, else null (the
     * picker then says "Track n").
     */
    fun name(locale: Locale): String? =
        label?.takeIf { it.isNotBlank() }
            ?: language?.takeIf { it.isNotBlank() && it != C.LANGUAGE_UNDETERMINED }
                ?.let { Locale.forLanguageTag(it).getDisplayName(locale).takeIf { n -> n.isNotBlank() && n != it } ?: it }
}

/** Every playable track of [type] in [tracks], in the stream's order. */
fun trackOptions(tracks: Tracks, type: Int): List<TrackOption> = buildList {
    tracks.groups.forEachIndexed { g, group ->
        if (group.type != type) return@forEachIndexed
        for (t in 0 until group.length) {
            if (!group.isTrackSupported(t)) continue
            val f = group.getTrackFormat(t)
            add(TrackOption(type, g, t, f.label, f.language, group.isTrackSelected(t)))
        }
    }
}

/**
 * The audio and subtitle choice a person made, kept for the next video (FR-34: "a choice is
 * kept for the next video in the series"): by language, since the next video's tracks are
 * other tracks. Null fields: no choice, the player's default.
 */
data class TrackChoice(
    val audioLanguage: String? = null,
    /** True: subtitles off; false: on in [textLanguage]; null: no choice made. */
    val textOff: Boolean? = null,
    val textLanguage: String? = null,
)

/**
 * The player's selection for a new video: the [choice] kept from the last one, else the
 * Settings' defaults (FR-31: subtitles off unless turned on, in the chosen language).
 */
fun TrackSelectionParameters.Builder.applyChoice(choice: TrackChoice, prefs: PlaybackValues): TrackSelectionParameters.Builder {
    clearOverrides()
    // sound is never turned off by a choice (FR-39a)
    setTrackTypeDisabled(C.TRACK_TYPE_AUDIO, false)
    choice.audioLanguage?.let { setPreferredAudioLanguage(it) }
    val textOn = choice.textOff?.not() ?: prefs.subtitlesOn
    setTrackTypeDisabled(C.TRACK_TYPE_TEXT, !textOn)
    if (textOn) {
        val lang = choice.textLanguage ?: prefs.subtitleLanguage.tag
        if (lang != null) setPreferredTextLanguage(lang)
        setSelectUndeterminedTextLanguage(true)
    }
    return this
}

/** Plays [option] now, and returns the choice to keep for the next video. */
@OptIn(UnstableApi::class)
fun Player.choose(option: TrackOption, kept: TrackChoice): TrackChoice {
    val group = currentTracks.groups.getOrNull(option.group) ?: return kept
    trackSelectionParameters = trackSelectionParameters.buildUpon()
        .setTrackTypeDisabled(option.type, false)
        .setOverrideForType(TrackSelectionOverride(group.mediaTrackGroup, option.track))
        .apply {
            if (option.type == C.TRACK_TYPE_TEXT) option.language?.let { setPreferredTextLanguage(it) }
            if (option.type == C.TRACK_TYPE_AUDIO) option.language?.let { setPreferredAudioLanguage(it) }
        }
        .build()
    return when (option.type) {
        C.TRACK_TYPE_TEXT -> kept.copy(textOff = false, textLanguage = option.language)
        C.TRACK_TYPE_AUDIO -> kept.copy(audioLanguage = option.language)
        else -> kept
    }
}

/** Turns subtitles off, and returns the choice to keep for the next video. */
fun Player.subtitlesOff(kept: TrackChoice): TrackChoice {
    trackSelectionParameters = trackSelectionParameters.buildUpon()
        .clearOverridesOfType(C.TRACK_TYPE_TEXT)
        .setTrackTypeDisabled(C.TRACK_TYPE_TEXT, true)
        .build()
    return kept.copy(textOff = true, textLanguage = null)
}

/**
 * Makes sure a stream with sound plays with sound (FR-39a): when [tracks] hold an audio track
 * this device plays and none is selected (audio turned off, or a kept language that only
 * matched nothing), the first one is chosen. True when it had to choose.
 */
@OptIn(UnstableApi::class)
fun Player.ensureAudio(tracks: Tracks = currentTracks): Boolean {
    if (volume == 0f) volume = 1f
    if (tracks.isTypeSelected(C.TRACK_TYPE_AUDIO)) return false
    val group = tracks.groups.firstOrNull { it.type == C.TRACK_TYPE_AUDIO && it.isSupported } ?: return false
    val track = (0 until group.length).first { group.isTrackSupported(it) }
    trackSelectionParameters = trackSelectionParameters.buildUpon()
        .setTrackTypeDisabled(C.TRACK_TYPE_AUDIO, false)
        .setOverrideForType(TrackSelectionOverride(group.mediaTrackGroup, track))
        .build()
    return true
}

/** A quality the picker offers (FR-35): Auto (no cap), or a height and the cap it sends. */
data class QualityOption(val height: Int?, val maxBitrateKbps: Int?) {
    companion object {
        val Auto = QualityOption(null, null)

        /**
         * The streaming service's own ladder (`streaming/internal/ffmpeg`): offered when a session
         * names none (direct play), so a person can still ask for less.
         */
        val Standard = listOf(Rendition(1080, 1920, 6000), Rendition(720, 1280, 3000), Rendition(480, 854, 1200))

        /** Auto, then each rung of [ladder] (else [Standard]) from the highest, one per height. */
        fun of(ladder: List<Rendition>): List<QualityOption> {
            val rungs = ladder.filter { it.height > 0 && it.bitrateKbps > 0 }.ifEmpty { Standard }
            return listOf(Auto) + rungs.sortedByDescending { it.height }.distinctBy { it.height }
                .map { QualityOption(it.height, it.bitrateKbps) }
        }
    }
}
