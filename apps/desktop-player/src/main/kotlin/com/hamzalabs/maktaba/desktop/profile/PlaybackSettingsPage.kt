package com.hamzalabs.maktaba.desktop.profile

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.FlowRow
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.FilterChip
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Switch
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
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
import com.hamzalabs.maktaba.desktop.mpv.EngineChoice
import com.hamzalabs.maktaba.desktop.mpv.Engines

/** A language choice's words. */
fun Texts.languageChoice(l: SubtitleLanguage): String = when (l) {
    SubtitleLanguage.Default -> this["profile.languageDefault"]
    SubtitleLanguage.English -> "English"
    SubtitleLanguage.Arabic -> "العربية"
}

/** A quality's words: Auto, or its height. */
fun Texts.qualityChoice(q: StreamQuality): String = q.height?.let { this["watch.qualityHeight", it] } ?: this["watch.qualityAuto"]

/** An engine choice's words (story 13.21). */
fun Texts.engineChoice(c: EngineChoice): String = when (c) {
    EngineChoice.Auto -> this["profile.engineAuto"]
    EngineChoice.Older -> this["profile.engineOlder"]
}

/**
 * Profile → Playback's **Video engine** row (story 13.21): its state ([Engines.row]), the
 * choice ([choose]: kept by the Player, applied to the next video) and **Learn more**, which
 * opens the help page's section on it.
 */
class EngineRow(val state: Engines.Row, val choose: (EngineChoice) -> Unit, val learnMore: () -> Unit)

/**
 * Profile's Playback (story 55.09): subtitles on by default, the subtitle and audio languages,
 * and the quality a video starts at. Kept in the Player's settings file; watching (55.08) reads
 * them. Then the video engine (13.21), when [engine] is given.
 */
@Composable
fun PlaybackSettingsPage(settings: PlaybackSettings, engine: EngineRow? = null) {
    val t = LocalTexts.current
    val v by settings.value.collectAsState()
    Column(verticalArrangement = Arrangement.spacedBy(16.dp), modifier = Modifier.verticalScroll(rememberScrollState()).testTag("playback-settings")) {
        Row(Modifier.fillMaxWidth(), verticalAlignment = Alignment.CenterVertically) {
            Text(t["profile.subtitlesOn"], modifier = Modifier.weight(1f))
            Switch(v.subtitlesOn, { on -> settings.update { it.copy(subtitlesOn = on) } }, modifier = Modifier.testTag("subtitles-on"))
        }
        Choices(t["profile.subtitleLanguage"], SubtitleLanguage.entries, v.subtitleLanguage, t::languageChoice, "subtitle-language") { l -> settings.update { it.copy(subtitleLanguage = l) } }
        Choices(t["profile.audioLanguage"], SubtitleLanguage.entries, v.audioLanguage, t::languageChoice, "audio-language") { l -> settings.update { it.copy(audioLanguage = l) } }
        Choices(t["profile.quality"], StreamQuality.entries, v.qualityWifi, t::qualityChoice, "quality") { q -> settings.update { it.copy(qualityWifi = q) } }
        if (engine != null) EngineChoices(engine)
    }
}

/**
 * Automatic or Older engine. The variable, or a libmpv that cannot load, locks it on the older
 * engine with the line that says why; the line on when to use it and **Learn more** follow.
 */
@Composable
private fun EngineChoices(row: EngineRow) {
    val t = LocalTexts.current
    val s = row.state
    Column(verticalArrangement = Arrangement.spacedBy(6.dp), modifier = Modifier.testTag("engine")) {
        Choices(t["profile.engine"], EngineChoice.entries, s.shown, t::engineChoice, "engine", enabled = s.lock == null, onChoose = row.choose)
        val locked = when (s.lock) {
            Engines.Why.Variable -> t["profile.engineVariable"]
            Engines.Why.Missing -> t["profile.engineMissing", s.reason.orEmpty()]
            else -> null
        }
        if (locked != null) Text(locked, color = MaterialTheme.colorScheme.onSurfaceVariant, modifier = Modifier.testTag("engine-locked"))
        Text(t["profile.engineAbout"], style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
        TextButton(onClick = row.learnMore, modifier = Modifier.testTag("engine-learn-more")) { Text(t["engine.learnMore"]) }
    }
}

@Composable
private fun <T : Enum<T>> Choices(
    title: String,
    options: List<T>,
    chosen: T,
    label: (T) -> String,
    tag: String,
    enabled: Boolean = true,
    onChoose: (T) -> Unit,
) {
    Column(verticalArrangement = Arrangement.spacedBy(6.dp)) {
        Text(title, style = MaterialTheme.typography.titleSmall)
        FlowRow(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
            for (o in options) {
                FilterChip(o == chosen, { onChoose(o) }, label = { Text(label(o)) }, enabled = enabled, modifier = Modifier.padding(0.dp).testTag("$tag-${o.name}"))
            }
        }
    }
}
