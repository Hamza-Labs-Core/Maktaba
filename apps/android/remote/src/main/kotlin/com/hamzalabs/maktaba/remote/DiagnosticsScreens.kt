package com.hamzalabs.maktaba.remote

import androidx.activity.compose.BackHandler
import androidx.compose.foundation.Canvas
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.ExperimentalLayoutApi
import androidx.compose.foundation.layout.FlowRow
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.LazyListScope
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.lazy.itemsIndexed
import androidx.compose.foundation.text.selection.SelectionContainer
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material3.Button
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.FilterChip
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.PrimaryScrollableTabRow
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Tab
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.material3.TopAppBar
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.platform.LocalLayoutDirection
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.style.TextDirection
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.LayoutDirection
import androidx.compose.ui.unit.dp
import androidx.compose.runtime.CompositionLocalProvider
import com.hamzalabs.maktaba.core.api.ApiException
import com.hamzalabs.maktaba.core.api.BOARD_RANGES
import com.hamzalabs.maktaba.core.api.Board
import com.hamzalabs.maktaba.core.api.BoardDoc
import com.hamzalabs.maktaba.core.api.BoardSeries
import com.hamzalabs.maktaba.core.api.ErrorFilter
import com.hamzalabs.maktaba.core.api.ErrorGroup
import com.hamzalabs.maktaba.core.api.ErrorGroupAction
import com.hamzalabs.maktaba.core.api.ErrorGroupDetail
import com.hamzalabs.maktaba.core.api.LogLine
import com.hamzalabs.maktaba.core.api.LogQuery
import com.hamzalabs.maktaba.core.api.LogSearch
import com.hamzalabs.maktaba.core.api.REPORT_STATUSES
import com.hamzalabs.maktaba.core.api.ReportDoc
import com.hamzalabs.maktaba.core.api.ReportSummary
import com.hamzalabs.maktaba.core.api.board
import com.hamzalabs.maktaba.core.api.errorGroup
import com.hamzalabs.maktaba.core.api.errorGroups
import com.hamzalabs.maktaba.core.api.gapsOverASecond
import com.hamzalabs.maktaba.core.api.problemReport
import com.hamzalabs.maktaba.core.api.problemReports
import com.hamzalabs.maktaba.core.api.replyToReport
import com.hamzalabs.maktaba.core.api.searchLogs
import com.hamzalabs.maktaba.core.api.setErrorStatus
import com.hamzalabs.maktaba.core.api.setReportStatus
import com.hamzalabs.maktaba.core.ui.reportCategory
import com.hamzalabs.maktaba.core.ui.reportStatus
import kotlinx.coroutines.launch
import com.hamzalabs.maktaba.core.R as CoreR

/**
 * The home's diagnostics on the Remote (Epic 53 stories 53.11 §4, 53.12 §4, 53.15 §5), from
 * the account menu: Errors (groups, a group's chart and lines, Mark seen, Resolve, Mute), Logs
 * (search; a reference follows its request), Dashboards (Overview and Storage, read only) and
 * Problem reports (the inbox). The routes are the web's; the home is the rule (admins only).
 */
enum class DiagTab(val label: Int) { Errors(R.string.diag_errors), Logs(R.string.diag_logs), Dashboards(R.string.diag_dashboards), Reports(R.string.diag_reports) }

/** What the diagnostics show over the tabs: a group, a line, a request, a report. */
sealed interface DiagPage {
    data class Group(val fp: String) : DiagPage
    data class Line(val id: Long?, val line: LogLine) : DiagPage
    data class Follow(val requestId: String) : DiagPage
    data class Report(val id: Int) : DiagPage
}

@OptIn(ExperimentalMaterial3Api::class, ExperimentalLayoutApi::class)
@Composable
fun DiagnosticsScreen(remote: Remote, onBack: () -> Unit, start: DiagTab = DiagTab.Errors) {
    var tab by rememberSaveable { mutableStateOf(start) }
    val pages = remember { mutableStateOf(listOf<DiagPage>()) }
    val top = pages.value.lastOrNull()
    val push: (DiagPage) -> Unit = { pages.value = pages.value + it }
    BackHandler { if (pages.value.isNotEmpty()) pages.value = pages.value.dropLast(1) else onBack() }
    Scaffold(
        modifier = Modifier.testTag("diagnostics"),
        topBar = {
            TopAppBar(
                title = { Text(stringResource(if (top == null) R.string.diag_title else pageTitle(top)), maxLines = 1, overflow = TextOverflow.Ellipsis) },
                navigationIcon = {
                    IconButton(onClick = { if (pages.value.isNotEmpty()) pages.value = pages.value.dropLast(1) else onBack() }, modifier = Modifier.testTag("diag-back")) {
                        Icon(Icons.AutoMirrored.Filled.ArrowBack, stringResource(CoreR.string.error_back))
                    }
                },
            )
        },
    ) { padding ->
        Column(Modifier.padding(padding).fillMaxSize()) {
            if (top == null) {
                PrimaryScrollableTabRow(selectedTabIndex = tab.ordinal, edgePadding = 8.dp) {
                    DiagTab.entries.forEach { t ->
                        Tab(selected = t == tab, onClick = { tab = t }, text = { Text(stringResource(t.label), maxLines = 1) },
                            modifier = Modifier.testTag("diag-tab-${t.name}"))
                    }
                }
                when (tab) {
                    DiagTab.Errors -> ErrorsList(remote, push)
                    DiagTab.Logs -> LogsList(remote, push)
                    DiagTab.Dashboards -> Dashboards(remote)
                    DiagTab.Reports -> ReportsList(remote, push)
                }
            } else {
                when (top) {
                    is DiagPage.Group -> GroupPage(remote, top.fp, push)
                    is DiagPage.Line -> LinePage(top.id, top.line, push)
                    is DiagPage.Follow -> FollowPage(remote, top.requestId, push)
                    is DiagPage.Report -> ReportPage(remote, top.id, push)
                }
            }
        }
    }
}

private fun pageTitle(p: DiagPage): Int = when (p) {
    is DiagPage.Group -> R.string.diag_error
    is DiagPage.Line -> R.string.diag_line
    is DiagPage.Follow -> R.string.diag_follow_title
    is DiagPage.Report -> R.string.diag_report
}

/** A load's state: loading, the value, or why not (a 503: the log index is down). */
private sealed interface Loaded<out T> {
    data object Loading : Loaded<Nothing>
    data class Ready<T>(val value: T) : Loaded<T>
    data class Failed(val indexDown: Boolean) : Loaded<Nothing>
}

@OptIn(ExperimentalLayoutApi::class)
@Composable
private fun <T> load(remote: Remote, vararg keys: Any?, block: suspend com.hamzalabs.maktaba.core.api.ApiClient.() -> T): Pair<Loaded<T>, () -> Unit> {
    var state by remember(*keys) { mutableStateOf<Loaded<T>>(Loaded.Loading) }
    var again by remember { mutableStateOf(0) }
    LaunchedEffect(again, *keys) {
        state = try {
            Loaded.Ready(remote.client.block())
        } catch (e: kotlinx.coroutines.CancellationException) {
            throw e
        } catch (e: Exception) {
            com.hamzalabs.maktaba.core.diagnostics.AppLog.failure("diagnostics", e)
            Loaded.Failed(e is ApiException.Status && e.code == 503)
        }
    }
    return state to { again++ }
}

private fun LazyListScope.status(state: Loaded<*>) {
    when (state) {
        Loaded.Loading -> item { Text(stringResource(R.string.diag_loading), Modifier.padding(16.dp)) }
        is Loaded.Failed -> item {
            Text(stringResource(if (state.indexDown) R.string.diag_index_down else R.string.diag_failed),
                color = MaterialTheme.colorScheme.error, modifier = Modifier.padding(16.dp).testTag("diag-problem"))
        }
        else -> {}
    }
}

private fun errorStatus(s: String): Int = when (s) {
    "seen" -> R.string.diag_status_seen
    "resolved" -> R.string.diag_status_resolved
    "muted" -> R.string.diag_status_muted
    else -> R.string.diag_status_new
}

@OptIn(ExperimentalLayoutApi::class)
@Composable
private fun ErrorsList(remote: Remote, push: (DiagPage) -> Unit) {
    var filter by rememberSaveable { mutableStateOf(ErrorFilter.Open) }
    val (state, _) = load(remote, filter) { errorGroups(filter) }
    LazyColumn(Modifier.fillMaxSize().testTag("diag-errors")) {
        item {
            FlowRow(Modifier.padding(horizontal = 16.dp, vertical = 8.dp), horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                listOf(ErrorFilter.Open to R.string.diag_filter_open, ErrorFilter.Resolved to R.string.diag_status_resolved,
                    ErrorFilter.Muted to R.string.diag_status_muted).forEach { (f, w) ->
                    FilterChip(selected = f == filter, onClick = { filter = f }, label = { Text(stringResource(w)) })
                }
            }
        }
        status(state)
        val groups = (state as? Loaded.Ready)?.value.orEmpty()
        if (state is Loaded.Ready && groups.isEmpty()) item { Text(stringResource(R.string.diag_no_errors), Modifier.padding(16.dp)) }
        items(groups, key = { it.fp }) { g -> ErrorRow(g) { push(DiagPage.Group(g.fp)) }; HorizontalDivider() }
    }
}

@OptIn(ExperimentalLayoutApi::class)
@Composable
private fun ErrorRow(g: ErrorGroup, onClick: () -> Unit) {
    Column(Modifier.fillMaxWidth().clickable(onClick = onClick).padding(16.dp).testTag("diag-group-${g.fp}"), verticalArrangement = Arrangement.spacedBy(4.dp)) {
        Row(verticalAlignment = Alignment.CenterVertically) {
            Text(g.title, style = MaterialTheme.typography.titleSmall, maxLines = 2, modifier = Modifier.weight(1f))
            Text(stringResource(errorStatus(g.status)), style = MaterialTheme.typography.labelMedium,
                color = if (g.status == "new") MaterialTheme.colorScheme.error else MaterialTheme.colorScheme.primary)
        }
        Text(g.part, style = MaterialTheme.typography.bodySmall.copy(textDirection = TextDirection.Ltr), fontFamily = FontFamily.Monospace)
        Row(verticalAlignment = Alignment.CenterVertically) {
            Text(stringResource(R.string.diag_counts, g.count24h, g.count, g.users), style = MaterialTheme.typography.bodySmall, modifier = Modifier.weight(1f))
            Bars(g.days.map { it.toDouble() }, Modifier.width(72.dp).height(18.dp))
        }
    }
}

/** Counts as bars, oldest first: time runs left to right in every language. */
@OptIn(ExperimentalLayoutApi::class)
@Composable
private fun Bars(values: List<Double>, modifier: Modifier) {
    val color = MaterialTheme.colorScheme.primary
    CompositionLocalProvider(LocalLayoutDirection provides LayoutDirection.Ltr) {
        Canvas(modifier) {
            val max = values.maxOrNull()?.takeIf { it > 0 } ?: return@Canvas
            val w = size.width / values.size
            values.forEachIndexed { i, v ->
                val h = (v / max * size.height).toFloat()
                drawRect(color, topLeft = Offset(i * w + w * 0.15f, size.height - h), size = androidx.compose.ui.geometry.Size(w * 0.7f, h))
            }
        }
    }
}

@OptIn(ExperimentalLayoutApi::class)
@Composable
private fun GroupPage(remote: Remote, fp: String, push: (DiagPage) -> Unit) {
    val (state, reload) = load(remote, fp) { errorGroup(fp) }
    val scope = rememberCoroutineScope()
    var busy by remember { mutableStateOf(false) }
    fun act(a: ErrorGroupAction) {
        busy = true
        scope.launch { remote.act { setErrorStatus(fp, a) }; busy = false; reload() }
    }
    LazyColumn(Modifier.fillMaxSize().testTag("diag-group")) {
        status(state)
        val d: ErrorGroupDetail = (state as? Loaded.Ready)?.value ?: return@LazyColumn
        item {
            Column(Modifier.padding(16.dp), verticalArrangement = Arrangement.spacedBy(6.dp)) {
                Text(d.group.title, style = MaterialTheme.typography.titleMedium)
                Text("${stringResource(errorStatus(d.group.status))} · ${d.group.part}", style = MaterialTheme.typography.bodySmall)
                Text(stringResource(R.string.diag_counts, d.group.count24h, d.group.count, d.group.users), style = MaterialTheme.typography.bodySmall)
                if (d.versions.isNotEmpty()) Text(stringResource(R.string.diag_releases, d.versions.joinToString(", ")), style = MaterialTheme.typography.bodySmall)
                if (d.machines.isNotEmpty()) Text(stringResource(R.string.diag_machines, d.machines.joinToString(", ")), style = MaterialTheme.typography.bodySmall)
                Text(stringResource(R.string.diag_last_week), style = MaterialTheme.typography.labelLarge)
                Bars(d.hourly.map { it.second.toDouble() }, Modifier.fillMaxWidth().height(80.dp))
                Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                    OutlinedButton(enabled = !busy && d.group.status != "seen", onClick = { act(ErrorGroupAction.Seen) }, modifier = Modifier.testTag("diag-seen")) {
                        Text(stringResource(R.string.diag_mark_seen))
                    }
                    Button(enabled = !busy, onClick = { act(ErrorGroupAction.Resolve(d.group.lastVersion)) }, modifier = Modifier.testTag("diag-resolve")) {
                        Text(stringResource(R.string.diag_resolve))
                    }
                }
                FlowRow(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                    val now = System.currentTimeMillis()
                    TextButton(enabled = !busy, onClick = { act(ErrorGroupAction.Mute(now + 86_400_000)) }) { Text(stringResource(R.string.diag_mute_day)) }
                    TextButton(enabled = !busy, onClick = { act(ErrorGroupAction.Mute(now + 7 * 86_400_000L)) }) { Text(stringResource(R.string.diag_mute_week)) }
                    TextButton(enabled = !busy, onClick = { act(ErrorGroupAction.Mute(null)) }) { Text(stringResource(R.string.diag_mute_resolved)) }
                }
                Text(stringResource(R.string.diag_recent_lines), style = MaterialTheme.typography.labelLarge)
            }
        }
        val rows = d.samples?.rows.orEmpty()
        if (rows.isEmpty()) item { Text(stringResource(R.string.diag_no_lines), Modifier.padding(16.dp)) }
        itemsIndexed(rows) { _, (id, line) -> LineRow(line) { push(DiagPage.Line(id, line)) }; HorizontalDivider() }
    }
}

@OptIn(ExperimentalLayoutApi::class)
@Composable
private fun LogsList(remote: Remote, push: (DiagPage) -> Unit) {
    var text by rememberSaveable { mutableStateOf("") }
    var query by remember { mutableStateOf(LogQuery()) }
    val (state, _) = load(remote, query) { searchLogs(query) }
    var more by remember(query) { mutableStateOf<List<Pair<Long?, LogLine>>>(emptyList()) }
    var cursor by remember(state) { mutableStateOf((state as? Loaded.Ready<LogSearch>)?.value?.cursor) }
    val scope = rememberCoroutineScope()
    LazyColumn(Modifier.fillMaxSize().testTag("diag-logs")) {
        item {
            Column(Modifier.padding(16.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
                OutlinedTextField(
                    value = text, onValueChange = { text = it }, singleLine = true,
                    label = { Text(stringResource(R.string.diag_search)) },
                    modifier = Modifier.fillMaxWidth().testTag("diag-search"),
                    keyboardActions = androidx.compose.foundation.text.KeyboardActions(onSearch = {
                        val ref = LogQuery.requestReference(text)
                        if (ref != null) push(DiagPage.Follow(ref)) else query = query.copy(text = text)
                    }),
                    keyboardOptions = androidx.compose.foundation.text.KeyboardOptions(imeAction = androidx.compose.ui.text.input.ImeAction.Search),
                )
                Text(stringResource(R.string.diag_search_hint), style = MaterialTheme.typography.bodySmall)
                FlowRow(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                    listOf("" to R.string.diag_level_all, "warn" to R.string.diag_level_warn, "error" to R.string.diag_level_error).forEach { (l, w) ->
                        FilterChip(selected = query.level == l, onClick = { query = query.copy(level = l) }, label = { Text(stringResource(w)) })
                    }
                }
                FlowRow(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                    LogQuery.WINDOWS.zip(listOf(R.string.diag_window_15m, R.string.diag_window_1h, R.string.diag_window_24h, R.string.diag_window_7d)).forEach { (m, w) ->
                        FilterChip(selected = query.windowMinutes == m, onClick = { query = query.copy(windowMinutes = m) }, label = { Text(stringResource(w)) })
                    }
                }
            }
        }
        status(state)
        val rows = (state as? Loaded.Ready)?.value?.rows.orEmpty() + more
        if (state is Loaded.Ready && rows.isEmpty()) item { Text(stringResource(R.string.diag_no_match), Modifier.padding(16.dp)) }
        itemsIndexed(rows) { _, (id, line) -> LineRow(line) { push(DiagPage.Line(id, line)) }; HorizontalDivider() }
        cursor?.let { c ->
            item {
                TextButton(onClick = {
                    scope.launch { remote.load { searchLogs(query, c) }?.let { more = more + it.rows; cursor = it.cursor } }
                }) { Text(stringResource(R.string.diag_more)) }
            }
        }
    }
}

@OptIn(ExperimentalLayoutApi::class)
@Composable
private fun LineRow(line: LogLine, onClick: () -> Unit) {
    Column(Modifier.fillMaxWidth().clickable(onClick = onClick).padding(horizontal = 16.dp, vertical = 8.dp)) {
        Row {
            Text(line.level.uppercase(), style = MaterialTheme.typography.labelSmall, fontFamily = FontFamily.Monospace,
                color = if (line.isError) MaterialTheme.colorScheme.error else MaterialTheme.colorScheme.primary)
            Text("  " + (line.component ?: line.service), style = MaterialTheme.typography.labelSmall, fontFamily = FontFamily.Monospace, modifier = Modifier.weight(1f))
            Text(line.ts?.substringAfter('T')?.take(8).orEmpty(), style = MaterialTheme.typography.labelSmall)
        }
        // as written: left to right in every language
        Text(line.msg, style = MaterialTheme.typography.bodyMedium.copy(textDirection = TextDirection.Ltr), fontFamily = FontFamily.Monospace, maxLines = 3)
    }
}

@OptIn(ExperimentalLayoutApi::class)
@Composable
private fun LinePage(id: Long?, line: LogLine, push: (DiagPage) -> Unit) {
    LazyColumn(Modifier.fillMaxSize().testTag("diag-line")) {
        item {
            Row(Modifier.padding(16.dp), horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                line.requestId?.let { r -> Button(onClick = { push(DiagPage.Follow(r)) }, modifier = Modifier.testTag("diag-follow")) { Text(stringResource(R.string.diag_follow)) } }
            }
        }
        items(line.texts.toList()) { (k, v) ->
            SelectionContainer {
                Column(Modifier.fillMaxWidth().padding(horizontal = 16.dp, vertical = 4.dp)) {
                    Text(k, style = MaterialTheme.typography.labelSmall.copy(textDirection = TextDirection.Ltr), fontFamily = FontFamily.Monospace)
                    Text(v, style = MaterialTheme.typography.bodySmall.copy(textDirection = TextDirection.Ltr), fontFamily = FontFamily.Monospace)
                }
            }
        }
    }
}

@OptIn(ExperimentalLayoutApi::class)
@Composable
private fun FollowPage(remote: Remote, requestId: String, push: (DiagPage) -> Unit) {
    val (state, _) = load(remote, requestId) { searchLogs(LogQuery(requestId = requestId)) }
    LazyColumn(Modifier.fillMaxSize().testTag("diag-follow-page")) {
        item { Text(requestId, Modifier.padding(16.dp), style = MaterialTheme.typography.labelMedium.copy(textDirection = TextDirection.Ltr), fontFamily = FontFamily.Monospace) }
        status(state)
        val rows = (state as? Loaded.Ready)?.value?.rows.orEmpty().sortedBy { it.second.at }
        if (state is Loaded.Ready && rows.isEmpty()) item { Text(stringResource(R.string.diag_follow_none), Modifier.padding(16.dp)) }
        val gaps = gapsOverASecond(rows.map { it.second })
        itemsIndexed(rows) { i, (id, line) ->
            if (gaps[i]) {
                val s = ((line.at!!.toEpochMilli() - rows[i - 1].second.at!!.toEpochMilli()) / 1000)
                Text(stringResource(R.string.diag_gap, s), style = MaterialTheme.typography.labelSmall, color = MaterialTheme.colorScheme.tertiary,
                    modifier = Modifier.padding(horizontal = 16.dp))
            }
            LineRow(line) { push(DiagPage.Line(id, line)) }
            HorizontalDivider()
        }
    }
}

/** A panel's title: the web's words for the metric, else its name. */
private fun seriesTitle(name: String): Int? = when (name) {
    "maktaba_component_up" -> R.string.dash_component_up
    "maktaba_component_restarts_total" -> R.string.dash_restarts
    "maktaba_errors_per_hour" -> R.string.dash_errors_per_hour
    "process_resident_memory_bytes" -> R.string.dash_api_memory
    "maktaba_supervisor_children" -> R.string.dash_processes
    "maktaba_logs_segments_bytes" -> R.string.dash_log_files
    "maktaba_logs_index_bytes" -> R.string.dash_log_index
    "maktaba_update_backup_bytes" -> R.string.dash_backups
    else -> null
}

private fun value(v: Double, name: String): String =
    if (name.contains("bytes")) bytes(v)
    else if (v == Math.floor(v)) v.toLong().toString() else "%.1f".format(v)

private fun bytes(v: Double): String {
    val units = listOf("B", "KB", "MB", "GB", "TB")
    var x = v
    var i = 0
    while (x >= 1000 && i < units.size - 1) { x /= 1000; i++ }
    return if (i == 0) "${x.toLong()} B" else "%.1f %s".format(x, units[i])
}

@OptIn(ExperimentalLayoutApi::class)
@Composable
private fun Dashboards(remote: Remote) {
    var board by rememberSaveable { mutableStateOf(Board.Overview) }
    var range by rememberSaveable { mutableStateOf("24h") }
    val (state, _) = load(remote, board, range) { board(board, range) }
    LazyColumn(Modifier.fillMaxSize().testTag("diag-dashboards")) {
        item {
            Column(Modifier.padding(16.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
                Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                    FilterChip(selected = board == Board.Overview, onClick = { board = Board.Overview }, label = { Text(stringResource(R.string.dash_overview)) })
                    FilterChip(selected = board == Board.Storage, onClick = { board = Board.Storage }, label = { Text(stringResource(R.string.dash_storage)) })
                }
                FlowRow(horizontalArrangement = Arrangement.spacedBy(6.dp)) {
                    BOARD_RANGES.zip(listOf(R.string.diag_window_1h, R.string.diag_window_24h, R.string.diag_window_7d, R.string.dash_30d, R.string.dash_90d)).forEach { (r, w) ->
                        FilterChip(selected = range == r, onClick = { range = r }, label = { Text(stringResource(w)) })
                    }
                }
            }
        }
        status(state)
        val doc: BoardDoc = (state as? Loaded.Ready)?.value ?: return@LazyColumn
        val panels = doc.panels.filter { (_, s) -> s.any { it.points.isNotEmpty() } }
        if (panels.isEmpty()) item { Text(stringResource(R.string.dash_no_data), Modifier.padding(16.dp)) }
        items(panels, key = { it.first }) { (name, series) -> Panel(name, series) }
    }
}

@OptIn(ExperimentalLayoutApi::class)
@Composable
private fun Panel(name: String, series: List<BoardSeries>) {
    val title = seriesTitle(name)?.let { stringResource(it) } ?: name
    val latest = series.sumOf { it.points.lastOrNull()?.getOrNull(1) ?: 0.0 }
    val max = series.flatMap { it.points }.maxOfOrNull { it.getOrNull(1) ?: 0.0 } ?: 0.0
    val summary = stringResource(R.string.dash_summary, title, value(latest, name), value(max, name))
    val colors = listOf(MaterialTheme.colorScheme.primary, MaterialTheme.colorScheme.tertiary, MaterialTheme.colorScheme.secondary, Color(0xFF6FA8DC))
    Column(Modifier.fillMaxWidth().padding(16.dp).testTag("dash-panel-$name")) {
        Text(title, style = MaterialTheme.typography.titleSmall)
        Text(summary, style = MaterialTheme.typography.bodySmall)
        CompositionLocalProvider(LocalLayoutDirection provides LayoutDirection.Ltr) {
            Canvas(Modifier.fillMaxWidth().height(120.dp).semantics { contentDescription = summary }) {
                val all = series.flatMap { it.points }.filter { it.size >= 2 }
                if (all.isEmpty()) return@Canvas
                val t0 = all.minOf { it[0] }; val t1 = all.maxOf { it[0] }.coerceAtLeast(t0 + 1)
                val v0 = minOf(0.0, all.minOf { it[1] }); val v1 = all.maxOf { it[1] }.coerceAtLeast(v0 + 1)
                series.forEachIndexed { si, s ->
                    val pts = s.points.filter { it.size >= 2 }.map {
                        Offset(((it[0] - t0) / (t1 - t0) * size.width).toFloat(), (size.height - (it[1] - v0) / (v1 - v0) * size.height).toFloat())
                    }
                    pts.zipWithNext().forEach { (a, b) -> drawLine(colors[si % colors.size], a, b, strokeWidth = 3f) }
                }
            }
        }
        if (series.size > 1) Text(series.joinToString(" · ") { it.legend }, style = MaterialTheme.typography.labelSmall)
    }
}

@OptIn(ExperimentalLayoutApi::class)
@Composable
private fun ReportsList(remote: Remote, push: (DiagPage) -> Unit) {
    val (state, _) = load(remote) { problemReports() }
    LazyColumn(Modifier.fillMaxSize().testTag("diag-reports")) {
        status(state)
        val reports = (state as? Loaded.Ready)?.value.orEmpty()
        if (state is Loaded.Ready && reports.isEmpty()) item { Text(stringResource(R.string.diag_no_reports), Modifier.padding(16.dp)) }
        items(reports, key = { it.id }) { r: ReportSummary ->
            Column(Modifier.fillMaxWidth().clickable { push(DiagPage.Report(r.id)) }.padding(16.dp).testTag("diag-report-${r.id}")) {
                Text("#${r.id} · ${stringResource(reportCategory(r.category))} · ${stringResource(reportStatus(r.status))}", style = MaterialTheme.typography.titleSmall)
                Text(listOfNotNull(r.name, r.app).joinToString(" · "), style = MaterialTheme.typography.bodySmall)
            }
            HorizontalDivider()
        }
    }
}

@OptIn(ExperimentalLayoutApi::class)
@Composable
private fun ReportPage(remote: Remote, id: Int, push: (DiagPage) -> Unit) {
    val (state, reload) = load(remote, id) { problemReport(id) }
    val scope = rememberCoroutineScope()
    var reply by rememberSaveable { mutableStateOf("") }
    LazyColumn(Modifier.fillMaxSize().testTag("diag-report")) {
        status(state)
        val d: ReportDoc = (state as? Loaded.Ready)?.value ?: return@LazyColumn
        item {
            Column(Modifier.padding(16.dp), verticalArrangement = Arrangement.spacedBy(6.dp)) {
                Text("#${d.id} · ${stringResource(reportCategory(d.category))}", style = MaterialTheme.typography.titleMedium)
                d.name?.let { Text(it) }
                Text("${d.app} ${d.version} · ${listOfNotNull(d.device, d.osVersion).joinToString(" · ")}",
                    style = MaterialTheme.typography.bodySmall.copy(textDirection = TextDirection.Ltr), fontFamily = FontFamily.Monospace)
                d.text?.takeIf { it.isNotBlank() }?.let { Text(it) }
                if (d.crashes.isNotEmpty()) Text(stringResource(R.string.diag_crashes, d.crashes.size), style = MaterialTheme.typography.bodySmall)
                FlowRow(horizontalArrangement = Arrangement.spacedBy(6.dp)) {
                    REPORT_STATUSES.forEach { s ->
                        FilterChip(selected = d.status == s, onClick = { scope.launch { remote.act { setReportStatus(id, s) }; reload() } },
                            label = { Text(stringResource(reportStatus(s))) }, modifier = Modifier.testTag("diag-report-status-$s"))
                    }
                }
                d.reply?.takeIf { it.isNotBlank() }?.let { Text(stringResource(CoreR.string.report_reply, it)) }
                OutlinedTextField(reply, { reply = it }, label = { Text(stringResource(R.string.diag_write_back)) }, modifier = Modifier.fillMaxWidth().testTag("diag-reply"))
                Button(enabled = reply.isNotBlank(), onClick = { scope.launch { if (remote.act { replyToReport(id, reply) }) reply = ""; reload() } },
                    modifier = Modifier.testTag("diag-reply-send")) { Text(stringResource(R.string.diag_send_reply)) }
                Text(stringResource(R.string.diag_timeline), style = MaterialTheme.typography.labelLarge)
            }
        }
        items(d.timeline) { (fromHome, line) ->
            Column {
                Text(stringResource(if (fromHome) R.string.diag_from_home else R.string.diag_from_app), style = MaterialTheme.typography.labelSmall,
                    modifier = Modifier.padding(start = 16.dp, top = 4.dp))
                LineRow(line) { line.requestId?.let { push(DiagPage.Follow(it)) } }
            }
        }
    }
}

/** The account menu's entry (Remote only). */
@Composable
fun DiagnosticsMenuItem(onOpen: () -> Unit) {
    androidx.compose.material3.DropdownMenuItem(text = { Text(stringResource(R.string.diag_title)) }, onClick = onOpen, modifier = Modifier.testTag("account-diagnostics"))
}
