package com.hamzalabs.maktaba.player.search

import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.FlowRow
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.navigationBarsPadding
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.outlined.Sort
import androidx.compose.material.icons.automirrored.outlined.TrendingUp
import androidx.compose.material.icons.outlined.Check
import androidx.compose.material.icons.outlined.Close
import androidx.compose.material.icons.outlined.FilterList
import androidx.compose.material.icons.outlined.History
import androidx.compose.material.icons.outlined.Person
import androidx.compose.material.icons.outlined.PlayCircle
import androidx.compose.material.icons.outlined.TextFields
import androidx.compose.material3.Badge
import androidx.compose.material3.BadgedBox
import androidx.compose.material3.Button
import androidx.compose.material3.DropdownMenu
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.FilterChip
import androidx.compose.material3.FilterChipDefaults
import androidx.compose.material3.Icon
import androidx.compose.material3.InputChip
import androidx.compose.material3.InputChipDefaults
import androidx.compose.material3.LinearProgressIndicator
import androidx.compose.material3.ListItem
import androidx.compose.material3.ListItemDefaults
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.ModalBottomSheet
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.platform.LocalConfiguration
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.res.pluralStringResource
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.semantics.clearAndSetSemantics
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.heading
import androidx.compose.ui.semantics.onClick
import androidx.compose.ui.semantics.role
import androidx.compose.ui.semantics.selected
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.text.style.TextDirection
import androidx.compose.ui.unit.dp
import com.hamzalabs.maktaba.core.api.Facet
import com.hamzalabs.maktaba.core.api.FacetBucket
import com.hamzalabs.maktaba.core.api.SearchSort
import com.hamzalabs.maktaba.core.api.SuggestKind
import com.hamzalabs.maktaba.core.api.Suggestion
import com.hamzalabs.maktaba.core.api.languageName
import com.hamzalabs.maktaba.player.R

// Story 49.11 on the Android Player: search's help (story 49.8's API) as the web shows it
// (story 49.10): suggestions with their kinds, Did you mean, the order, and the facets in a
// sheet (a phone's way to keep the results in view), with the filters in use above the results.

/** A suggestion: its kind's icon and word (none from an older home), then its text. */
@Composable
internal fun SuggestionRow(s: Suggestion, onClick: () -> Unit) {
    val kind = s.kind
    val kindWord = kind?.let { stringResource(kindText(it)) }
    ListItem(
        headlineContent = { Text(s.text, style = MaterialTheme.typography.bodyLarge.copy(textDirection = TextDirection.Content)) },
        supportingContent = kindWord?.let { { Text(it) } },
        leadingContent = { Icon(kindIcon(kind), contentDescription = null) },
        colors = ListItemDefaults.colors(containerColor = MaterialTheme.colorScheme.surfaceContainerHigh),
        modifier = Modifier
            .testTag("suggestion")
            .clickable(role = Role.Button, onClick = onClick),
    )
}

private fun kindText(k: SuggestKind): Int = when (k) {
    SuggestKind.History -> R.string.search_suggest_kind_history
    SuggestKind.Term -> R.string.search_suggest_kind_term
    SuggestKind.Title -> R.string.search_suggest_kind_title
    SuggestKind.Speaker -> R.string.search_suggest_kind_speaker
}

private fun kindIcon(k: SuggestKind?): ImageVector = when (k) {
    SuggestKind.History -> Icons.Outlined.History
    SuggestKind.Term -> Icons.Outlined.TextFields
    SuggestKind.Title -> Icons.Outlined.PlayCircle
    SuggestKind.Speaker -> Icons.Outlined.Person
    null -> Icons.AutoMirrored.Outlined.TrendingUp
}

/** The words of an order. */
@Composable
internal fun sortName(s: SearchSort): String = stringResource(
    when (s) {
        SearchSort.Relevance -> R.string.search_sort_relevance
        SearchSort.Newest -> R.string.search_sort_newest
        SearchSort.Matches -> R.string.search_sort_matches
    },
)

/** A facet's name. */
@Composable
internal fun facetName(f: Facet): String = stringResource(
    when (f) {
        Facet.Library -> R.string.search_facet_library
        Facet.Language -> R.string.search_facet_language
        Facet.Speaker -> R.string.search_facet_speaker
        Facet.Year -> R.string.search_facet_year
        Facet.Duration -> R.string.search_facet_duration
    },
)

/**
 * The name a bucket is shown by, in the app's language: a library's name ([label], else the one
 * seen before in [libraries]), a language's name, a year, a band's words, a speaker as named.
 */
@Composable
internal fun bucketName(f: Facet, value: String, label: String?, libraries: Map<String, String>): String {
    val locale = LocalConfiguration.current.locales[0]
    return when (f) {
        Facet.Library -> label?.trim()?.takeIf { it.isNotEmpty() } ?: libraries[value] ?: value
        Facet.Language -> remember(value, locale) { languageName(value, locale) }
        Facet.Year -> value.toIntOrNull()?.let { String.format(locale, "%d", it) } ?: value
        Facet.Duration -> when (value) {
            "under_5m" -> stringResource(R.string.search_duration_under_5m, 5)
            "5m_20m" -> stringResource(R.string.search_duration_5m_20m, 5, 20)
            "20m_60m" -> stringResource(R.string.search_duration_20m_60m, 20, 60)
            "over_60m" -> stringResource(R.string.search_duration_over_60m)
            else -> value
        }
        Facet.Speaker -> value
    }
}

/**
 * Above the results: the order (a menu), Filters (the sheet; its badge counts the filters in
 * use), then the filters in use, each removable, and Clear all filters, then Did you mean.
 * A thin bar runs while a refined search is on its way (the results stay until it answers).
 */
@Composable
internal fun RefineBar(model: SearchModel, answer: SearchAnswer, onSort: (SearchSort) -> Unit, onToggle: (Facet, FacetBucket) -> Unit, onRemove: (Facet, String) -> Unit, onClear: () -> Unit, onDidYouMean: (String) -> Unit) {
    var sheet by rememberSaveable { mutableStateOf(false) }
    val choice = model.choice
    val hasFacets = Facet.entries.any { choice.shown(answer.facets, it).isNotEmpty() }
    Column(Modifier.fillMaxWidth().testTag("search-refine")) {
        Row(
            Modifier.fillMaxWidth().padding(horizontal = 16.dp, vertical = 4.dp),
            horizontalArrangement = Arrangement.spacedBy(8.dp),
            verticalAlignment = Alignment.CenterVertically,
        ) {
            SortMenu(answer.sort, onSort)
            if (hasFacets) {
                val label = stringResource(R.string.search_filters)
                val described = if (choice.isEmpty) label else stringResource(R.string.search_filters_in_use, choice.count)
                FilterChip(
                    selected = !choice.isEmpty,
                    onClick = { sheet = true },
                    label = { Text(label) },
                    leadingIcon = {
                        BadgedBox(badge = { if (!choice.isEmpty) Badge { Text(String.format(LocalConfiguration.current.locales[0], "%d", choice.count)) } }) {
                            Icon(Icons.Outlined.FilterList, contentDescription = null, Modifier.size(FilterChipDefaults.IconSize))
                        }
                    },
                    modifier = Modifier.testTag("search-filters").semantics { contentDescription = described },
                )
            }
        }
        if (!choice.isEmpty) ActiveFilters(model, onRemove, onClear)
        answer.didYouMean?.let { DidYouMean(it, onDidYouMean) }
        if (model.searching) LinearProgressIndicator(Modifier.fillMaxWidth().testTag("search-refining"))
    }
    if (sheet) {
        FacetSheet(model, answer, onToggle, onClear, onDismiss = { sheet = false })
    }
}

/** The order: a chip that opens the three, the current one ticked. */
@Composable
private fun SortMenu(current: SearchSort, onSort: (SearchSort) -> Unit) {
    var open by remember { mutableStateOf(false) }
    Box {
        FilterChip(
            selected = false,
            onClick = { open = true },
            label = { Text(stringResource(R.string.search_sort_current, sortName(current))) },
            leadingIcon = { Icon(Icons.AutoMirrored.Outlined.Sort, contentDescription = null, Modifier.size(FilterChipDefaults.IconSize)) },
            modifier = Modifier.testTag("search-sort"),
        )
        DropdownMenu(expanded = open, onDismissRequest = { open = false }) {
            for (s in SearchSort.entries) {
                val on = s == current
                DropdownMenuItem(
                    text = { Text(sortName(s)) },
                    onClick = { open = false; onSort(s) },
                    leadingIcon = { if (on) Icon(Icons.Outlined.Check, contentDescription = null) else Box(Modifier.size(24.dp)) },
                    modifier = Modifier.testTag("search-sort-${s.wire}").semantics { selected = on },
                )
            }
        }
    }
}

/** "Filters in use": each as "Facet: value" with its remove button, then Clear all filters. */
@Composable
private fun ActiveFilters(model: SearchModel, onRemove: (Facet, String) -> Unit, onClear: () -> Unit) {
    val heading = stringResource(R.string.search_filters_active)
    FlowRow(
        Modifier.fillMaxWidth().padding(horizontal = 16.dp).testTag("search-active-filters").semantics { contentDescription = heading },
        horizontalArrangement = Arrangement.spacedBy(8.dp),
        verticalArrangement = Arrangement.spacedBy(0.dp),
        itemVerticalAlignment = Alignment.CenterVertically,
    ) {
        for ((f, v) in model.choice.chosen) {
            val text = stringResource(R.string.search_filter_chip, facetName(f), bucketName(f, v, null, model.libraryNames))
            val remove = stringResource(R.string.search_filter_remove, text)
            InputChip(
                selected = true,
                onClick = { onRemove(f, v) },
                label = { Text(text, maxLines = 1) },
                trailingIcon = { Icon(Icons.Outlined.Close, contentDescription = null, Modifier.size(InputChipDefaults.IconSize)) },
                modifier = Modifier.testTag("active-${f.wire}-$v").clearAndSetSemantics {
                    contentDescription = remove
                    role = Role.Button
                    onClick { onRemove(f, v); true }
                },
            )
        }
        TextButton(onClick = onClear, modifier = Modifier.testTag("search-filters-clear")) { Text(stringResource(R.string.search_filters_clear)) }
    }
}

/** "Did you mean …?": the corrected words are a button that runs them. */
@Composable
private fun DidYouMean(q: String, onClick: (String) -> Unit) {
    val sentence = stringResource(R.string.search_did_you_mean, q)
    val (before, after) = stringResource(R.string.search_did_you_mean, "\u0000").split("\u0000").let { it[0] to it.getOrElse(1) { "" } }
    Row(
        Modifier.fillMaxWidth().padding(horizontal = 16.dp).testTag("search-did-you-mean"),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        if (before.isNotBlank()) Text(before.trim(), style = MaterialTheme.typography.bodyLarge)
        TextButton(
            onClick = { onClick(q) },
            modifier = Modifier.testTag("search-did-you-mean-run").semantics { contentDescription = sentence },
        ) { Text(q, style = MaterialTheme.typography.bodyLarge.copy(textDirection = TextDirection.Content)) }
        if (after.isNotBlank()) Text(after.trim(), style = MaterialTheme.typography.bodyLarge)
    }
}

/**
 * Narrow the results: each facet a group of chips with its count. A press runs the search again
 * at once and the counts follow; a chosen bucket the narrowed facets lost stays, pressed,
 * without a count. Show results closes the sheet.
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
private fun FacetSheet(model: SearchModel, answer: SearchAnswer, onToggle: (Facet, FacetBucket) -> Unit, onClear: () -> Unit, onDismiss: () -> Unit) {
    ModalBottomSheet(onDismissRequest = onDismiss, modifier = Modifier.testTag("search-facets")) {
        Column(
            Modifier.fillMaxWidth().verticalScroll(rememberScrollState()).padding(horizontal = 16.dp).navigationBarsPadding(),
            verticalArrangement = Arrangement.spacedBy(12.dp),
        ) {
            Text(stringResource(R.string.search_facets), style = MaterialTheme.typography.titleLarge, modifier = Modifier.semantics { heading() })
            if (model.searching) LinearProgressIndicator(Modifier.fillMaxWidth())
            for (f in Facet.entries) {
                val buckets = model.choice.shown(answer.facets, f)
                if (buckets.isEmpty()) continue
                Column(verticalArrangement = Arrangement.spacedBy(4.dp)) {
                    Text(facetName(f), style = MaterialTheme.typography.titleSmall, color = MaterialTheme.colorScheme.primary, modifier = Modifier.semantics { heading() })
                    FlowRow(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                        for (b in buckets) FacetChip(model, f, b, onToggle)
                    }
                }
            }
            Row(Modifier.fillMaxWidth().padding(vertical = 8.dp), horizontalArrangement = Arrangement.spacedBy(8.dp, Alignment.End)) {
                if (!model.choice.isEmpty) TextButton(onClick = onClear, modifier = Modifier.testTag("facets-clear")) { Text(stringResource(R.string.search_filters_clear)) }
                Button(onClick = onDismiss, modifier = Modifier.testTag("facets-done")) { Text(stringResource(R.string.search_filters_done)) }
            }
        }
    }
}

/** One bucket: its name and count; pressed when chosen. TalkBack reads "name, N videos". */
@Composable
private fun FacetChip(model: SearchModel, f: Facet, b: FacetBucket, onToggle: (Facet, FacetBucket) -> Unit) {
    val name = bucketName(f, b.value, b.label, model.libraryNames)
    val on = model.choice.has(f, b.value)
    val locale = LocalConfiguration.current.locales[0]
    val count = if (b.counted) pluralStringResource(R.plurals.search_facet_videos, b.count, b.count) else null
    FilterChip(
        selected = on,
        onClick = { onToggle(f, b) },
        label = {
            Row(horizontalArrangement = Arrangement.spacedBy(6.dp), verticalAlignment = Alignment.CenterVertically) {
                Text(name, maxLines = 1, style = MaterialTheme.typography.labelLarge.copy(textDirection = TextDirection.Content))
                if (b.counted) Text(String.format(locale, "%d", b.count), style = MaterialTheme.typography.labelMedium, color = MaterialTheme.colorScheme.onSurfaceVariant)
            }
        },
        leadingIcon = if (on) {
            { Icon(Icons.Outlined.Check, contentDescription = null, Modifier.size(FilterChipDefaults.IconSize)) }
        } else {
            null
        },
        modifier = Modifier.testTag("facet-${f.wire}-${b.value}").clearAndSetSemantics {
            contentDescription = listOfNotNull(name, count).joinToString(", ")
            role = Role.Checkbox
            selected = on
            onClick { onToggle(f, b); true }
        },
    )
}
