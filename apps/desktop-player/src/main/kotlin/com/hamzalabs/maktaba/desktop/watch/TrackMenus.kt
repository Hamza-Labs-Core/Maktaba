package com.hamzalabs.maktaba.desktop.watch

import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.material3.MaterialTheme
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.widthIn
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Audiotrack
import androidx.compose.material.icons.filled.Check
import androidx.compose.material.icons.filled.ClosedCaption
import androidx.compose.material.icons.filled.HighQuality
import androidx.compose.material3.DropdownMenu
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.unit.dp
import com.hamzalabs.maktaba.core.playback.QualityOption
import com.hamzalabs.maktaba.desktop.AudioOption
import com.hamzalabs.maktaba.desktop.LocalTexts
import com.hamzalabs.maktaba.desktop.Texts
import com.hamzalabs.maktaba.desktop.shell.FeatureOffNote
import java.util.Locale

/** A track's name: its own, else its language in [lang]'s words, else "Track n". */
fun Texts.trackName(name: String?, language: String?, n: Int): String =
    name?.takeIf { it.isNotBlank() }
        ?: language?.takeIf { it.isNotBlank() && it != "und" }?.let {
            Locale.forLanguageTag(twoLetter(it)).getDisplayName(Locale.forLanguageTag(lang.code)).takeIf { d -> d.isNotBlank() && d != it } ?: it
        }
        ?: this["watch.trackUnknown", n]

/** ISO 639-2's three letters (a file's tracks: "fra", "fre", "ger") as their two, which every locale names. */
private val THREE_TO_TWO: Map<String, String> by lazy {
    val m = HashMap<String, String>()
    for (two in Locale.getISOLanguages()) runCatching { m[Locale(two).isO3Language] = two }
    // the bibliographic codes some files carry
    m += mapOf("fre" to "fr", "ger" to "de", "dut" to "nl", "chi" to "zh", "per" to "fa", "cze" to "cs", "gre" to "el", "rum" to "ro", "slo" to "sk", "alb" to "sq", "arm" to "hy", "baq" to "eu", "bur" to "my", "geo" to "ka", "ice" to "is", "mac" to "mk", "mao" to "mi", "may" to "ms", "tib" to "bo", "wel" to "cy")
    m
}

private fun twoLetter(code: String): String = code.lowercase().let { c -> if (c.length == 3) THREE_TO_TWO[c] ?: c else code }

/** A control's button and its menu. */
@Composable
private fun MenuButton(icon: ImageVector, label: String, tag: String, content: @Composable (close: () -> Unit) -> Unit) {
    var open by remember { mutableStateOf(false) }
    Box {
        IconButton(onClick = { open = true }, modifier = Modifier.testTag(tag)) { Icon(icon, label, tint = Color.White) }
        DropdownMenu(expanded = open, onDismissRequest = { open = false }) { content { open = false } }
    }
}

@Composable
private fun Choice(text: String, chosen: Boolean, tag: String, note: String? = null, onClick: () -> Unit) {
    DropdownMenuItem(
        text = {
            if (note == null) Text(text)
            else Column {
                Text(text)
                Text(note, style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
            }
        },
        onClick = onClick,
        leadingIcon = { if (chosen) Icon(Icons.Filled.Check, null) },
        modifier = Modifier.testTag(tag),
    )
}

/**
 * The Subtitles menu's names for [options] (story 13.20): each track's by [trackName]; a name
 * that two of them share gets its track number after it.
 */
fun Texts.subtitleNames(options: List<SubtitleOption>): List<String> {
    val names = options.mapIndexed { i, o -> trackName(o.name, o.language, i + 1) }
    val twice = names.groupingBy { it }.eachCount().filterValues { it > 1 }.keys
    return names.mapIndexed { i, n -> if (n in twice) "$n (${i + 1})" else n }
}

/** What the menu says under a subtitle's name: **In the file** for the file's own, and **Picture** for a picture track. */
fun Texts.subtitleNote(o: SubtitleOption): String? = when {
    o.kind != SubtitleOption.Kind.File -> null
    o.picture -> this["watch.inFile"] + " · " + this["watch.picture"]
    else -> this["watch.inFile"]
}

/**
 * Subtitles: Off and each subtitle track (the menu scrolls when they are many). [offNote] (an
 * admin, with subtitles off on the home) puts the off note in the menu; a viewer with subtitles
 * off never sees this control.
 */
@Composable
fun SubtitlesMenu(options: List<SubtitleOption>, chosen: SubtitleOption?, onChoose: (SubtitleOption?) -> Unit, offNote: String? = null, homeUrl: String = "") {
    val t = LocalTexts.current
    MenuButton(Icons.Filled.ClosedCaption, t["watch.subtitles"], "subtitles") { close ->
        if (offNote != null) Box(Modifier.widthIn(max = 360.dp).padding(8.dp)) { FeatureOffNote(offNote, homeUrl) }
        Choice(t["watch.subtitlesOff"], chosen == null, "subtitles-off") { onChoose(null); close() }
        val names = t.subtitleNames(options)
        options.forEachIndexed { i, o -> Choice(names[i], o == chosen, "subtitles-$i", note = t.subtitleNote(o)) { onChoose(o); close() } }
    }
}

/** Audio: each audio track of the stream (no menu when it has one). */
@Composable
fun AudioMenu(options: List<AudioOption>, chosen: AudioOption?, onChoose: (AudioOption) -> Unit) {
    val t = LocalTexts.current
    MenuButton(Icons.Filled.Audiotrack, t["watch.audio"], "audio") { close ->
        options.forEachIndexed { i, o -> Choice(t.trackName(o.name, o.language, i + 1), o == chosen, "audio-$i") { onChoose(o); close() } }
    }
}

/** Quality: Auto and the session's ladder; the choice is marked by its height (a cap from the settings is not a rung's). */
@Composable
fun QualityMenu(options: List<QualityOption>, chosen: QualityOption, onChoose: (QualityOption) -> Unit) {
    val t = LocalTexts.current
    MenuButton(Icons.Filled.HighQuality, t["watch.quality"], "quality") { close ->
        options.forEach { q ->
            val label = q.height?.let { t["watch.qualityHeight", it] } ?: t["watch.qualityAuto"]
            Choice(label, q.height == chosen.height, "quality-${q.height ?: "auto"}") { onChoose(q); close() }
        }
    }
}
