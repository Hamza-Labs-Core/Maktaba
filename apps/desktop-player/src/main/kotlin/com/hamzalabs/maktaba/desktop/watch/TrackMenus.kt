package com.hamzalabs.maktaba.desktop.watch

import androidx.compose.foundation.layout.Box
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
            Locale.forLanguageTag(it).getDisplayName(Locale.forLanguageTag(lang.code)).takeIf { d -> d.isNotBlank() && d != it } ?: it
        }
        ?: this["watch.trackUnknown", n]

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
private fun Choice(text: String, chosen: Boolean, tag: String, onClick: () -> Unit) {
    DropdownMenuItem(
        text = { Text(text) },
        onClick = onClick,
        leadingIcon = { if (chosen) Icon(Icons.Filled.Check, null) },
        modifier = Modifier.testTag(tag),
    )
}

/**
 * Subtitles: Off and each subtitle track. [offNote] (an admin, with subtitles off on the home)
 * puts the off note in the menu; a viewer with subtitles off never sees this control.
 */
@Composable
fun SubtitlesMenu(options: List<Rendition>, chosen: Rendition?, onChoose: (Rendition?) -> Unit, offNote: String? = null, homeUrl: String = "") {
    val t = LocalTexts.current
    MenuButton(Icons.Filled.ClosedCaption, t["watch.subtitles"], "subtitles") { close ->
        if (offNote != null) Box(Modifier.widthIn(max = 360.dp).padding(8.dp)) { FeatureOffNote(offNote, homeUrl) }
        Choice(t["watch.subtitlesOff"], chosen == null, "subtitles-off") { onChoose(null); close() }
        options.forEachIndexed { i, o -> Choice(t.trackName(o.name, o.language, i + 1), o == chosen, "subtitles-$i") { onChoose(o); close() } }
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

/** Quality: Auto and the session's ladder. */
@Composable
fun QualityMenu(options: List<QualityOption>, chosen: QualityOption, onChoose: (QualityOption) -> Unit) {
    val t = LocalTexts.current
    MenuButton(Icons.Filled.HighQuality, t["watch.quality"], "quality") { close ->
        options.forEach { q ->
            val label = q.height?.let { t["watch.qualityHeight", it] } ?: t["watch.qualityAuto"]
            Choice(label, q == chosen, "quality-${q.height ?: "auto"}") { onChoose(q); close() }
        }
    }
}
