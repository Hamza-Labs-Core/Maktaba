package com.hamzalabs.maktaba.core.ui

import android.graphics.Bitmap
import android.graphics.BitmapFactory
import android.graphics.Canvas
import android.view.View
import androidx.activity.compose.BackHandler
import androidx.compose.foundation.Image
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.selection.selectable
import androidx.compose.foundation.text.selection.SelectionContainer
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Button
import androidx.compose.material3.Checkbox
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.RadioButton
import androidx.compose.material3.Surface
import androidx.compose.material3.Switch
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.runtime.staticCompositionLocalOf
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.asImageBitmap
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.LocalView
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.text.style.TextDirection
import androidx.compose.ui.unit.dp
import androidx.compose.ui.window.Dialog
import androidx.compose.ui.window.DialogProperties
import com.hamzalabs.maktaba.core.R
import com.hamzalabs.maktaba.core.api.ApiClient
import com.hamzalabs.maktaba.core.api.ApiException
import com.hamzalabs.maktaba.core.api.REPORT_CATEGORIES
import com.hamzalabs.maktaba.core.api.ReportSummary
import com.hamzalabs.maktaba.core.api.RequestId
import com.hamzalabs.maktaba.core.api.problemReports
import com.hamzalabs.maktaba.core.api.sendProblemReport
import com.hamzalabs.maktaba.core.crash.CrashOutbox
import com.hamzalabs.maktaba.core.crash.ReportPart
import com.hamzalabs.maktaba.core.diagnostics.AppLog
import com.hamzalabs.maktaba.core.diagnostics.CrashConsent
import com.hamzalabs.maktaba.core.diagnostics.CrashReports
import com.hamzalabs.maktaba.core.diagnostics.Redact
import com.hamzalabs.maktaba.core.session.AppSession
import com.hamzalabs.maktaba.core.session.Home
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.launch
import java.io.ByteArrayOutputStream

// Epic 53 on Android: crash reports to the home (53.14: the one-time question, the switch,
// sending) and Report a problem (53.15: the form, My reports, an error screen's Report this).

/** Opens Report a problem, with an error screen's reference when there is one. */
val LocalReportProblem = staticCompositionLocalOf<((String?) -> Unit)?> { null }

/** The words of a report's category and status. */
fun reportCategory(c: String): Int = when (c) {
    "playback" -> R.string.report_category_playback
    "live_tv" -> R.string.report_category_live
    "search" -> R.string.report_category_search
    "signing_in" -> R.string.report_category_signin
    else -> R.string.report_category_other
}

fun reportStatus(s: String): Int = when (s) {
    "looking" -> R.string.report_status_looking
    "fixed" -> R.string.report_status_fixed
    "closed" -> R.string.report_status_closed
    else -> R.string.report_status_new
}

/**
 * Crash reports and Report a problem for everything under it, signed in to [home] through
 * [client]: asks the question once (two equal buttons, neither chosen), sends what is due when
 * the person said yes, says once what happened to the last crash, and gives [LocalReportProblem].
 */
@Composable
fun DiagnosticsHost(client: ApiClient, home: Home?, tv: Boolean = false, content: @Composable () -> Unit) {
    val context = LocalContext.current
    val view = LocalView.current
    val consent = remember { CrashConsent(context) }
    var asking by remember(home) { mutableStateOf(false) }
    var notice by remember { mutableStateOf<CrashOutbox.Notice?>(null) }
    var reporting by remember { mutableStateOf<Pair<String?, ByteArray?>?>(null) }
    suspend fun send() {
        if (home == null) return
        CrashReports.flush(context, client, home, tv)
        if (CrashConsent.questionAllowed) notice = CrashReports.outbox(context).notice()
    }
    LaunchedEffect(home, client) {
        if (home == null) return@LaunchedEffect
        if (consent.answer(home) == null) {
            asking = CrashConsent.questionAllowed
        } else {
            runCatching { send() }
        }
    }
    val scope = rememberCoroutineScope()
    CompositionLocalProvider(LocalReportProblem provides { ref -> reporting = ref to Screenshot.of(view) }) { content() }
    if (asking && home != null) {
        CrashQuestion(home.label) { yes ->
            consent.set(yes, home)
            asking = false
            scope.launch { runCatching { send() } }
        }
    }
    notice?.let { n ->
        AlertDialog(
            onDismissRequest = { notice = null },
            confirmButton = { TextButton(onClick = { notice = null }) { Text(stringResource(R.string.close)) } },
            text = {
                Text(if (n == CrashOutbox.Notice.Sent) stringResource(R.string.crash_last_sent, home?.label.orEmpty()) else stringResource(R.string.crash_last_kept))
            },
        )
    }
    reporting?.let { (ref, shot) ->
        ReportProblemDialog(client, home?.label.orEmpty(), ref, shot, tv) { reporting = null }
    }
}

/** The one-time question (53.14 §3). */
@Composable
fun CrashQuestion(home: String, answer: (Boolean) -> Unit) {
    Dialog(onDismissRequest = {}, properties = DialogProperties(dismissOnBackPress = false, dismissOnClickOutside = false)) {
        Surface(shape = MaterialTheme.shapes.extraLarge, modifier = Modifier.testTag("crash-question")) {
            Column(Modifier.padding(24.dp), verticalArrangement = Arrangement.spacedBy(16.dp)) {
                Text(stringResource(R.string.crash_question_title, home), style = MaterialTheme.typography.headlineSmall)
                Text(stringResource(R.string.crash_question_body, home), style = MaterialTheme.typography.bodyMedium)
                // the same kind and size: neither is the default
                Row(horizontalArrangement = Arrangement.spacedBy(12.dp)) {
                    OutlinedButton(onClick = { answer(true) }, modifier = Modifier.weight(1f).testTag("crash-question-yes")) {
                        Text(stringResource(R.string.crash_question_yes), textAlign = TextAlign.Center)
                    }
                    OutlinedButton(onClick = { answer(false) }, modifier = Modifier.weight(1f).testTag("crash-question-no")) {
                        Text(stringResource(R.string.crash_question_no), textAlign = TextAlign.Center)
                    }
                }
            }
        }
    }
}

/** "Send crash reports to {home}", off until the person turns it on. */
@Composable
fun CrashReportsSwitch(client: ApiClient?, home: Home, tv: Boolean = false, modifier: Modifier = Modifier) {
    val context = LocalContext.current
    val consent = remember { CrashConsent(context) }
    var on by remember(home) { mutableStateOf(consent.isOn(home)) }
    val scope = rememberCoroutineScope()
    Row(modifier.fillMaxWidth().padding(16.dp), verticalAlignment = Alignment.CenterVertically) {
        Column(Modifier.weight(1f)) {
            Text(stringResource(R.string.crash_setting, home.label))
            Text(stringResource(R.string.crash_setting_help, home.label), style = MaterialTheme.typography.bodySmall)
        }
        Spacer(Modifier.width(12.dp))
        Switch(
            checked = on,
            onCheckedChange = { v ->
                on = v
                consent.set(v, home)
                if (v && client != null) scope.launch { runCatching { CrashReports.flush(context, client, home, tv) } }
            },
            modifier = Modifier.testTag("crash-setting"),
        )
    }
}

/** A picture of the screen before the form covers it (JPEG under 1 MB); sent only when ticked. */
object Screenshot {
    fun of(view: View): ByteArray? = runCatching {
        val root = view.rootView
        if (root.width <= 0 || root.height <= 0) return null
        val bmp = Bitmap.createBitmap(root.width, root.height, Bitmap.Config.ARGB_8888)
        root.draw(Canvas(bmp))
        listOf(80, 60, 40, 20).firstNotNullOfOrNull { q ->
            ByteArrayOutputStream().also { bmp.compress(Bitmap.CompressFormat.JPEG, q, it) }.toByteArray()
                .takeIf { it.size <= ReportPart.SCREENSHOT_MAX }
        }
    }.getOrNull()
}

/** The form (53.15 §2; on a TV, §3: a category and Send are enough with the remote). */
@Composable
fun ReportProblemDialog(client: ApiClient, home: String, reference: String?, screenshot: ByteArray?, tv: Boolean = false, onDismiss: () -> Unit) {
    Dialog(onDismissRequest = onDismiss, properties = DialogProperties(usePlatformDefaultWidth = false)) {
        Surface(Modifier.fillMaxSize()) {
            ReportProblemForm(client, home, reference, screenshot, tv, onDismiss)
        }
    }
}

@Composable
fun ReportProblemForm(client: ApiClient, home: String, reference: String?, screenshot: ByteArray?, tv: Boolean, onDone: () -> Unit) {
    val context = LocalContext.current
    var category by remember { mutableStateOf("other") }
    var text by remember { mutableStateOf("") }
    var earlier by remember { mutableStateOf(false) }
    var withCrashes by remember { mutableStateOf(true) }
    var withShot by remember { mutableStateOf(false) }
    var showLog by remember { mutableStateOf(false) }
    var sending by remember { mutableStateOf(false) }
    var number by remember { mutableStateOf<Int?>(null) }
    var problem by remember { mutableStateOf<String?>(null) }
    val lines = remember { ReportPart.lines(AppLog.ring?.lines().orEmpty(), System.currentTimeMillis(), Redact::scrub) }
    val outbox = remember { CrashReports.outbox(context) }
    val crashCount = remember { outbox.files().size }
    val header = remember { CrashReports.header(context, tv) }
    val scope = rememberCoroutineScope()
    val limitWords = stringResource(R.string.report_limit)
    val failedWords = stringResource(R.string.report_failed)
    BackHandler(onBack = onDone)
    val n = number
    if (n != null) {
        Column(Modifier.fillMaxSize().padding(32.dp), verticalArrangement = Arrangement.spacedBy(16.dp, Alignment.CenterVertically),
            horizontalAlignment = Alignment.CenterHorizontally) {
            Text(stringResource(R.string.report_sent_to, home, n), style = MaterialTheme.typography.titleLarge,
                textAlign = TextAlign.Center, modifier = Modifier.testTag("report-sent"))
            Text(stringResource(R.string.report_reply_note), textAlign = TextAlign.Center)
            Button(onClick = onDone, modifier = Modifier.testTag("report-done")) { Text(stringResource(R.string.close)) }
        }
        return
    }
    Column(Modifier.fillMaxSize().verticalScroll(rememberScrollState()).padding(24.dp).testTag("report-form"), verticalArrangement = Arrangement.spacedBy(12.dp)) {
        Text(stringResource(R.string.report_title), style = MaterialTheme.typography.headlineSmall)
        Text(stringResource(R.string.report_what), style = MaterialTheme.typography.titleMedium)
        REPORT_CATEGORIES.forEach { c ->
            Row(Modifier.fillMaxWidth().selectable(selected = category == c, role = Role.RadioButton, onClick = { category = c }).testTag("report-category-$c"),
                verticalAlignment = Alignment.CenterVertically) {
                RadioButton(selected = category == c, onClick = null)
                Text(stringResource(reportCategory(c)), Modifier.padding(start = 8.dp))
            }
        }
        OutlinedTextField(value = text, onValueChange = { text = it.take(ReportPart.TEXT_MAX) }, label = { Text(stringResource(R.string.report_describe)) },
            modifier = Modifier.fillMaxWidth().heightIn(min = 96.dp).testTag("report-text"))
        if (!tv) {
            Row(verticalAlignment = Alignment.CenterVertically) {
                RadioButton(selected = !earlier, onClick = { earlier = false }, modifier = Modifier.testTag("report-when-now"))
                Text(stringResource(R.string.report_when_now))
                Spacer(Modifier.width(16.dp))
                RadioButton(selected = earlier, onClick = { earlier = true }, modifier = Modifier.testTag("report-when-earlier"))
                Text(stringResource(R.string.report_when_earlier))
            }
        }
        HorizontalDivider()
        Text(stringResource(R.string.report_sent_header), style = MaterialTheme.typography.titleMedium)
        Text(stringResource(R.string.report_device))
        Text("${header.app} ${header.appVersion} · ${header.deviceModel} · ${header.osVersion}",
            style = MaterialTheme.typography.bodySmall.copy(textDirection = TextDirection.Ltr), fontFamily = FontFamily.Monospace)
        TextButton(onClick = { showLog = !showLog }, modifier = Modifier.testTag("report-log")) {
            Text(stringResource(R.string.report_log, lines.size))
        }
        if (showLog) {
            SelectionContainer {
                Column(Modifier.heightIn(max = 320.dp).verticalScroll(rememberScrollState()).testTag("report-preview")) {
                    if (lines.isEmpty()) Text(stringResource(R.string.report_no_lines))
                    // as they will be sent: scrubbed, left to right in every language
                    lines.forEach { Text(it, style = MaterialTheme.typography.labelSmall.copy(textDirection = TextDirection.Ltr), fontFamily = FontFamily.Monospace) }
                }
            }
        }
        Text(stringResource(R.string.report_requests, minOf(20, RequestId.recent().size)))
        if (crashCount > 0) {
            Row(verticalAlignment = Alignment.CenterVertically) {
                Checkbox(checked = withCrashes, onCheckedChange = { withCrashes = it }, modifier = Modifier.testTag("report-crashes"))
                Text(stringResource(R.string.report_crashes, crashCount))
            }
        }
        if (screenshot != null) {
            Row(verticalAlignment = Alignment.CenterVertically) {
                Checkbox(checked = withShot, onCheckedChange = { withShot = it }, modifier = Modifier.testTag("report-screenshot"))
                Column {
                    Text(stringResource(R.string.report_screenshot))
                    Text(stringResource(R.string.report_screenshot_help), style = MaterialTheme.typography.bodySmall)
                }
            }
            if (withShot) {
                remember(screenshot) { BitmapFactory.decodeByteArray(screenshot, 0, screenshot.size)?.asImageBitmap() }?.let {
                    Image(it, contentDescription = stringResource(R.string.report_screenshot), modifier = Modifier.fillMaxWidth().testTag("report-screenshot-preview"))
                }
                TextButton(onClick = { withShot = false }, modifier = Modifier.testTag("report-screenshot-remove")) {
                    Text(stringResource(R.string.report_screenshot_remove))
                }
            }
        }
        HorizontalDivider()
        Text(stringResource(R.string.report_to_home, home))
        problem?.let { Text(it, color = MaterialTheme.colorScheme.error, modifier = Modifier.testTag("report-problem")) }
        Row(horizontalArrangement = Arrangement.spacedBy(12.dp)) {
            Button(
                enabled = !sending,
                onClick = {
                    sending = true
                    problem = null
                    scope.launch {
                        try {
                            val now = System.currentTimeMillis()
                            val part = ReportPart.part(category, text, if (earlier) now - 3_600_000 else null, lines,
                                if (withCrashes) outbox.events(Redact::scrub) else emptyList(), header, RequestId.recent(),
                                reference, withShot && screenshot != null, Redact::scrub)
                            number = client.sendProblemReport(part, if (withShot) screenshot else null)
                        } catch (e: CancellationException) {
                            throw e
                        } catch (e: ApiException.Status) {
                            problem = if (e.code == 429) limitWords else failedWords
                        } catch (e: Exception) {
                            AppLog.failure("problem report", e)
                            problem = failedWords
                        } finally {
                            sending = false
                        }
                    }
                },
                modifier = Modifier.testTag("report-send"),
            ) {
                if (sending) CircularProgressIndicator(Modifier.width(18.dp)) else Text(stringResource(R.string.report_send))
            }
            OutlinedButton(onClick = onDone) { Text(stringResource(R.string.cancel)) }
        }
    }
}

/** My reports (53.15 §5): the person's own, their status and the admin's reply. */
@Composable
fun MyReportsDialog(client: ApiClient, onDismiss: () -> Unit) {
    Dialog(onDismissRequest = onDismiss, properties = DialogProperties(usePlatformDefaultWidth = false)) {
        Surface(Modifier.fillMaxSize()) { MyReports(client, onDismiss) }
    }
}

@Composable
fun MyReports(client: ApiClient, onDone: () -> Unit) {
    var reports by remember { mutableStateOf<List<ReportSummary>?>(null) }
    var failed by remember { mutableStateOf(false) }
    BackHandler(onBack = onDone)
    LaunchedEffect(client) {
        try {
            reports = client.problemReports()
        } catch (e: CancellationException) {
            throw e
        } catch (e: Exception) {
            AppLog.failure("my reports", e)
            failed = true
        }
    }
    Column(Modifier.fillMaxSize().padding(24.dp).testTag("my-reports"), verticalArrangement = Arrangement.spacedBy(12.dp)) {
        Text(stringResource(R.string.report_mine), style = MaterialTheme.typography.headlineSmall)
        when {
            failed -> Text(stringResource(R.string.report_failed), color = MaterialTheme.colorScheme.error)
            reports == null -> CircularProgressIndicator()
            reports!!.isEmpty() -> Text(stringResource(R.string.report_none))
        }
        LazyColumn(Modifier.weight(1f)) {
            items(reports.orEmpty(), key = { it.id }) { r ->
                Column(Modifier.fillMaxWidth().padding(vertical = 8.dp).testTag("my-report-${r.id}")) {
                    Text("#${r.id} · ${stringResource(reportCategory(r.category))} · ${stringResource(reportStatus(r.status))}",
                        style = MaterialTheme.typography.titleSmall)
                    r.reply?.takeIf { it.isNotBlank() }?.let { Text(stringResource(R.string.report_reply, it)) }
                }
                HorizontalDivider()
            }
        }
        OutlinedButton(onClick = onDone) { Text(stringResource(R.string.close)) }
    }
}

/** The account menu's diagnostics items' state: who is signed in where (or null). */
@Composable
internal fun signedInClient(session: AppSession): ApiClient? =
    (session.phase.collectAsState().value as? AppSession.Phase.SignedIn)?.client
