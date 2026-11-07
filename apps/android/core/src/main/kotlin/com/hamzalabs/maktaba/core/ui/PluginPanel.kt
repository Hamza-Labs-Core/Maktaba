package com.hamzalabs.maktaba.core.ui

import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.ExperimentalLayoutApi
import androidx.compose.foundation.layout.FlowRow
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.widthIn
import androidx.compose.material3.AssistChip
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedCard
import androidx.compose.material3.SuggestionChip
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.semantics.heading
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.unit.dp
import com.hamzalabs.maktaba.core.R
import com.hamzalabs.maktaba.core.api.PanelBlock
import com.hamzalabs.maktaba.core.api.PanelText
import com.hamzalabs.maktaba.core.api.PanelView

// Plugin panels (Epic 54, story 54.20): a plugin's read-only view, declared as data and drawn
// here with Compose's own components. Every value is a plain `Text` from a String (no
// annotated string, no link, no HTML): the home refused anything else when the plugin wrote it,
// and PanelText removes control and direction characters again. No value makes a request: the
// panel image is the home's route, loaded by the app's own image loader (`image`). The TV shows
// kv, badges and markers only.

/** "From plugins", collapsed, below Maktaba's own sections; nothing when there is no panel. */
@Composable
fun PluginPanels(
    panels: List<PanelView>,
    modifier: Modifier = Modifier,
    tv: Boolean = false,
    onSeek: ((Double) -> Unit)? = null,
    onSearch: ((String) -> Unit)? = null,
    image: (@Composable (String) -> Unit)? = null,
) {
    if (panels.isEmpty()) return
    var open by rememberSaveable { mutableStateOf(tv) }
    Column(modifier.fillMaxWidth().testTag("plugin-panels")) {
        TextButton(onClick = { open = !open }, modifier = Modifier.testTag("plugin-panels-toggle")) {
            Text(stringResource(R.string.plugin_panels_title), Modifier.semantics { heading() }, style = MaterialTheme.typography.titleMedium)
        }
        if (open) {
            panels.forEach { PluginPanel(it, tv = tv, onSeek = onSeek, onSearch = onSearch, image = image) }
        }
    }
}

/** One plugin's panel. */
@OptIn(ExperimentalLayoutApi::class)
@Composable
fun PluginPanel(
    panel: PanelView,
    tv: Boolean = false,
    onSeek: ((Double) -> Unit)? = null,
    onSearch: ((String) -> Unit)? = null,
    image: (@Composable (String) -> Unit)? = null,
) {
    OutlinedCard(Modifier.fillMaxWidth().padding(vertical = 6.dp).testTag("plugin-panel-${panel.pluginId}")) {
        Column(Modifier.padding(16.dp), verticalArrangement = Arrangement.spacedBy(10.dp)) {
            Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                Text(PanelText.plain(panel.title.ifBlank { panel.plugin }, 100), style = MaterialTheme.typography.titleSmall)
                if (panel.title.isNotBlank()) {
                    Text(PanelText.plain(panel.plugin, 100), style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
                }
            }
            PanelText.shown(panel.blocks, tv).forEach { Block(it, onSeek, onSearch, image) }
        }
    }
}

@OptIn(ExperimentalLayoutApi::class)
@Composable
private fun Block(b: PanelBlock, onSeek: ((Double) -> Unit)?, onSearch: ((String) -> Unit)?, image: (@Composable (String) -> Unit)?) {
    Column(verticalArrangement = Arrangement.spacedBy(4.dp)) {
        b.label?.takeIf { it.isNotBlank() }?.let { Text(PanelText.plain(it, 100), style = MaterialTheme.typography.labelLarge) }
        when (b.type) {
            "kv" -> PanelText.list(b.value).take(20).forEach { row ->
                Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.spacedBy(12.dp)) {
                    Text(PanelText.plain(PanelText.field(row, "label")), Modifier.widthIn(min = 96.dp), color = MaterialTheme.colorScheme.onSurfaceVariant)
                    Text(PanelText.plain(PanelText.field(row, "value")))
                }
            }
            "badges" -> FlowRow(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                PanelText.list(b.value).take(10).forEach { SuggestionChip(onClick = {}, label = { Text(PanelText.plain(PanelText.field(it, "text"))) }, enabled = false) }
            }
            "chips" -> FlowRow(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                PanelText.list(b.value).take(20).map { PanelText.plain(it) }.filter { it.isNotEmpty() }.forEach { text ->
                    AssistChip(onClick = { onSearch?.invoke(text) }, label = { Text(text) }, enabled = onSearch != null)
                }
            }
            "table" -> {
                val cols = b.columns.take(6)
                Row(Modifier.fillMaxWidth()) { cols.forEach { Text(PanelText.plain(it, 100), Modifier.weight(1f), style = MaterialTheme.typography.labelMedium) } }
                PanelText.list(b.value).take(50).forEach { row ->
                    val cells = PanelText.list(row)
                    Row(Modifier.fillMaxWidth()) { cols.indices.forEach { j -> Text(PanelText.plain(cells.getOrNull(j)), Modifier.weight(1f)) } }
                }
            }
            "markers" -> PanelText.list(b.value).take(200).forEach { m ->
                val t = PanelText.number(PanelText.field(m, "t_ms"))?.takeIf { it >= 0 } ?: return@forEach
                Row(
                    Modifier.fillMaxWidth().let { if (onSeek != null) it.clickable { onSeek(t / 1000) } else it }.padding(vertical = 4.dp),
                    horizontalArrangement = Arrangement.spacedBy(12.dp),
                ) {
                    Text(PanelText.clock(t), color = MaterialTheme.colorScheme.primary)
                    Text(PanelText.plain(PanelText.field(m, "label")))
                }
            }
            "image" -> PanelText.safeImage(b.image)?.let { src -> image?.invoke(src) }
            "text" -> Text(PanelText.plain(b.value, 1000))
        }
    }
}
