package com.hamzalabs.maktaba.desktop.profile

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.FlowRow
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.material3.FilterChip
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Switch
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.unit.dp
import com.hamzalabs.maktaba.core.playback.PlaybackSettings
import com.hamzalabs.maktaba.core.playback.StreamQuality
import com.hamzalabs.maktaba.core.playback.SubtitleLanguage
import com.hamzalabs.maktaba.desktop.LocalTexts
import com.hamzalabs.maktaba.desktop.Texts

/** A language choice's words. */
fun Texts.languageChoice(l: SubtitleLanguage): String = when (l) {
    SubtitleLanguage.Default -> this["profile.languageDefault"]
    SubtitleLanguage.English -> "English"
    SubtitleLanguage.Arabic -> "العربية"
}

/** A quality's words: Auto, or its height. */
fun Texts.qualityChoice(q: StreamQuality): String = q.height?.let { this["watch.qualityHeight", it] } ?: this["watch.qualityAuto"]

/**
 * Profile's Playback (story 55.09): subtitles on by default, the subtitle and audio languages,
 * and the quality a video starts at. Kept in the Player's settings file; watching (55.08) reads them.
 */
@Composable
fun PlaybackSettingsPage(settings: PlaybackSettings) {
    val t = LocalTexts.current
    val v by settings.value.collectAsState()
    Column(verticalArrangement = Arrangement.spacedBy(16.dp), modifier = Modifier.testTag("playback-settings")) {
        Row(Modifier.fillMaxWidth(), verticalAlignment = Alignment.CenterVertically) {
            Text(t["profile.subtitlesOn"], modifier = Modifier.weight(1f))
            Switch(v.subtitlesOn, { on -> settings.update { it.copy(subtitlesOn = on) } }, modifier = Modifier.testTag("subtitles-on"))
        }
        Choices(t["profile.subtitleLanguage"], SubtitleLanguage.entries, v.subtitleLanguage, t::languageChoice, "subtitle-language") { l -> settings.update { it.copy(subtitleLanguage = l) } }
        Choices(t["profile.audioLanguage"], SubtitleLanguage.entries, v.audioLanguage, t::languageChoice, "audio-language") { l -> settings.update { it.copy(audioLanguage = l) } }
        Choices(t["profile.quality"], StreamQuality.entries, v.qualityWifi, t::qualityChoice, "quality") { q -> settings.update { it.copy(qualityWifi = q) } }
    }
}

@Composable
private fun <T : Enum<T>> Choices(title: String, options: List<T>, chosen: T, label: (T) -> String, tag: String, onChoose: (T) -> Unit) {
    Column(verticalArrangement = Arrangement.spacedBy(6.dp)) {
        Text(title, style = MaterialTheme.typography.titleSmall)
        FlowRow(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
            for (o in options) FilterChip(o == chosen, { onChoose(o) }, label = { Text(label(o)) }, modifier = Modifier.padding(0.dp).testTag("$tag-${o.name}"))
        }
    }
}
