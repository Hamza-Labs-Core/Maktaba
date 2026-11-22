package com.hamzalabs.maktaba.desktop.search

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.ExperimentalLayoutApi
import androidx.compose.foundation.layout.FlowRow
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.size
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.Sort
import androidx.compose.material.icons.filled.Check
import androidx.compose.material.icons.filled.Close
import androidx.compose.material.icons.filled.History
import androidx.compose.material.icons.filled.Person
import androidx.compose.material.icons.filled.PlayCircle
import androidx.compose.material.icons.filled.Search
import androidx.compose.material.icons.filled.TextFields
import androidx.compose.material3.DropdownMenu
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.FilterChip
import androidx.compose.material3.FilterChipDefaults
import androidx.compose.material3.Icon
import androidx.compose.material3.InputChip
import androidx.compose.material3.InputChipDefaults
import androidx.compose.material3.LinearProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.clearAndSetSemantics
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.heading
import androidx.compose.ui.semantics.onClick
import androidx.compose.ui.semantics.role
import androidx.compose.ui.semantics.selected
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.unit.dp
import com.hamzalabs.maktaba.core.api.Facet
import com.hamzalabs.maktaba.core.api.FacetBucket
import com.hamzalabs.maktaba.core.api.SearchSort
import com.hamzalabs.maktaba.core.api.SuggestKind
import com.hamzalabs.maktaba.core.api.languageName
import com.hamzalabs.maktaba.desktop.LocalTexts
import com.hamzalabs.maktaba.desktop.Texts
import java.util.Locale

// Story 49.11 on Windows and Linux: search's help (story 49.8's API) as the web shows it (story
// 49.10): suggestions with their kinds, Did you mean, the order, the facets as chips above the
// results (a desktop window has the room; they scroll away with the results), and the filters in
// use, each removable. Every chip is reached with Tab and pressed with Enter or Space.

/** A suggestion kind's icon (an older home's suggestions have none). */
internal fun kindIcon(k: SuggestKind?): ImageVector = when (k) {
    SuggestKind.History -> Icons.Filled.History
    SuggestKind.Term -> Icons.Filled.TextFields
    SuggestKind.Title -> Icons.Filled.PlayCircle
    SuggestKind.Speaker -> Icons.Filled.Person
    null -> Icons.Filled.Search
}

/** A suggestion kind's word. */
internal fun Texts.kind(k: SuggestKind): String = this["search.kind." + k.wire]

internal fun Texts.sortName(s: SearchSort): String = this["search.sort." + s.wire]

/** A list's comma: Arabic's in Arabic. */
internal val Texts.comma: String get() = if (lang == com.hamzalabs.maktaba.desktop.Lang.Ar) "، " else ", "

internal fun Texts.facetName(f: Facet): String = this["search.facet." + f.wire]

/** "N videos" (one: "1 video"). */
internal fun Texts.videos(n: Int): String = if (n == 1) this["search.facet.countOne"] else this["search.facet.count", n]

/**
 * The name a bucket is shown by, in the app's language: a library's name ([label], else the one
 * seen before in [libraries]), a language's name, a year, a band's words, a speaker as named.
 */
internal fun Texts.bucketName(f: Facet, value: String, label: String?, libraries: Map<String, String>): String = when (f) {
    Facet.Library -> label?.trim()?.takeIf { it.isNotEmpty() } ?: libraries[value] ?: value
    Facet.Language -> languageName(value, Locale.forLanguageTag(lang.code))
    Facet.Duration -> if (has("search.duration.$value")) this["search.duration.$value"] else value
    Facet.Year, Facet.Speaker -> value
}

/**
 * Above the results: Did you mean, the order, the filters in use with Clear all filters, then
 * each facet a row of chips with its counts. A thin bar runs while a refined search is on its way.
 */
@OptIn(ExperimentalLayoutApi::class)
@Composable
internal fun Refine(model: SearchModel, answer: SearchAnswer, onSort: (SearchSort) -> Unit, onToggle: (Facet, FacetBucket) -> Unit, onRemove: (Facet, String) -> Unit, onClear: () -> Unit, onDidYouMean: () -> Unit) {
    val t = LocalTexts.current
    Column(Modifier.fillMaxWidth().testTag("search-refine"), verticalArrangement = Arrangement.spacedBy(8.dp)) {
        answer.didYouMean?.let { DidYouMean(it, onDidYouMean) }
        Row(horizontalArrangement = Arrangement.spacedBy(8.dp), verticalAlignment = Alignment.CenterVertically) {
            SortMenu(answer.sort, onSort)
        }
        if (!model.choice.isEmpty) {
            val heading = t["search.filters.active"]
            FlowRow(
                Modifier.fillMaxWidth().testTag("search-active-filters").semantics { contentDescription = heading },
                horizontalArrangement = Arrangement.spacedBy(8.dp),
                verticalArrangement = Arrangement.spacedBy(4.dp),
            ) {
                for ((f, v) in model.choice.chosen) {
                    val text = t["search.filters.chip", t.facetName(f), t.bucketName(f, v, null, model.libraryNames)]
                    val remove = t["search.filters.remove", text]
                    InputChip(
                        selected = true,
                        onClick = { onRemove(f, v) },
                        label = { Text(text, maxLines = 1) },
                        trailingIcon = { Icon(Icons.Filled.Close, null, Modifier.size(InputChipDefaults.IconSize)) },
                        modifier = Modifier.testTag("active-${f.wire}-$v").clearAndSetSemantics {
                            contentDescription = remove
                            role = Role.Button
                            onClick { onRemove(f, v); true }
                        },
                    )
                }
                TextButton(onClick = onClear, modifier = Modifier.testTag("search-filters-clear")) { Text(t["search.filters.clear"]) }
            }
        }
        val groups = Facet.entries.map { it to model.choice.shown(answer.facets, it) }.filter { it.second.isNotEmpty() }
        if (groups.isNotEmpty()) {
            Column(Modifier.fillMaxWidth().testTag("search-facets"), verticalArrangement = Arrangement.spacedBy(4.dp)) {
                Text(t["search.facets"], style = MaterialTheme.typography.titleSmall, modifier = Modifier.semantics { heading() })
                for ((f, buckets) in groups) {
                    FlowRow(horizontalArrangement = Arrangement.spacedBy(8.dp), verticalArrangement = Arrangement.spacedBy(4.dp)) {
                        Text(t.facetName(f), style = MaterialTheme.typography.labelLarge, color = MaterialTheme.colorScheme.primary, modifier = Modifier.align(Alignment.CenterVertically).semantics { heading() })
                        for (b in buckets) FacetChip(model, f, b, onToggle)
                    }
                }
            }
        }
        if (model.searching) LinearProgressIndicator(Modifier.fillMaxWidth().testTag("search-refining"))
    }
}

/** "Did you mean …?": the corrected words are a button that runs them. */
@Composable
private fun DidYouMean(q: String, onClick: () -> Unit) {
    val t = LocalTexts.current
    val (before, after) = t["search.didYouMean", "\u0000"].split("\u0000").let { it[0] to it.getOrElse(1) { "" } }
    Row(Modifier.testTag("search-did-you-mean"), verticalAlignment = Alignment.CenterVertically) {
        if (before.isNotBlank()) Text(before.trim())
        TextButton(onClick = onClick, modifier = Modifier.testTag("search-did-you-mean-run").semantics { contentDescription = t["search.didYouMean", q] }) { Text(q) }
        if (after.isNotBlank()) Text(after.trim())
    }
}

/** The order: a chip that opens the three, the current one ticked. */
@Composable
private fun SortMenu(current: SearchSort, onSort: (SearchSort) -> Unit) {
    val t = LocalTexts.current
    var open by remember { mutableStateOf(false) }
    Box {
        FilterChip(
            selected = false,
            onClick = { open = true },
            label = { Text(t["search.sort.current", t.sortName(current)]) },
            leadingIcon = { Icon(Icons.AutoMirrored.Filled.Sort, null, Modifier.size(FilterChipDefaults.IconSize)) },
            modifier = Modifier.testTag("search-sort"),
        )
        DropdownMenu(expanded = open, onDismissRequest = { open = false }) {
            for (s in SearchSort.entries) {
                val on = s == current
                DropdownMenuItem(
                    text = { Text(t.sortName(s)) },
                    onClick = { open = false; onSort(s) },
                    leadingIcon = { if (on) Icon(Icons.Filled.Check, null) else Box(Modifier.size(24.dp)) },
                    modifier = Modifier.testTag("search-sort-${s.wire}").semantics { selected = on },
                )
            }
        }
    }
}

/** One bucket: its name and count; pressed when chosen. A screen reader reads "name, N videos". */
@Composable
private fun FacetChip(model: SearchModel, f: Facet, b: FacetBucket, onToggle: (Facet, FacetBucket) -> Unit) {
    val t = LocalTexts.current
    val name = t.bucketName(f, b.value, b.label, model.libraryNames)
    val on = model.choice.has(f, b.value)
    FilterChip(
        selected = on,
        onClick = { onToggle(f, b) },
        label = {
            Row(horizontalArrangement = Arrangement.spacedBy(6.dp), verticalAlignment = Alignment.CenterVertically) {
                Text(name, maxLines = 1)
                if (b.counted) Text(b.count.toString(), style = MaterialTheme.typography.labelMedium, color = MaterialTheme.colorScheme.onSurfaceVariant)
            }
        },
        leadingIcon = if (on) {
            { Icon(Icons.Filled.Check, null, Modifier.size(FilterChipDefaults.IconSize)) }
        } else {
            null
        },
        modifier = Modifier.testTag("facet-${f.wire}-${b.value}").clearAndSetSemantics {
            contentDescription = listOfNotNull(name, if (b.counted) t.videos(b.count) else null).joinToString(t.comma)
            role = Role.Checkbox
            selected = on
            onClick { onToggle(f, b); true }
        },
    )
}
