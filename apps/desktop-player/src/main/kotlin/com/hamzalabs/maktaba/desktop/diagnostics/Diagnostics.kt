package com.hamzalabs.maktaba.desktop.diagnostics

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.selection.selectable
import androidx.compose.foundation.text.selection.SelectionContainer
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Checkbox
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.RadioButton
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.style.TextDirection
import androidx.compose.ui.unit.dp
import com.hamzalabs.maktaba.core.api.ApiClient
import com.hamzalabs.maktaba.core.api.ApiException
import com.hamzalabs.maktaba.core.api.REPORT_CATEGORIES
import com.hamzalabs.maktaba.core.api.ReportSummary
import com.hamzalabs.maktaba.core.api.RequestId
import com.hamzalabs.maktaba.core.api.problemReports
import com.hamzalabs.maktaba.core.api.sendCrashEnvelope
import com.hamzalabs.maktaba.core.api.sendProblemReport
import com.hamzalabs.maktaba.core.crash.CrashOutbox
import com.hamzalabs.maktaba.core.crash.Envelope
import com.hamzalabs.maktaba.core.crash.KeptCrash
import com.hamzalabs.maktaba.core.crash.ReportPart
import com.hamzalabs.maktaba.core.diagnostics.Redact
import com.hamzalabs.maktaba.core.session.Home
import com.hamzalabs.maktaba.core.session.KeyValueStore
import com.hamzalabs.maktaba.desktop.LocalTexts
import com.hamzalabs.maktaba.desktop.Strings
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.launch
import java.io.File
import java.util.UUID

// Epic 53 on the desktop Player (Windows and Linux): crash reports to the home (53.14) and
// Report a problem (53.15), with the Android apps' envelope, outbox and report part (`client`).

/**
 * Whether this person, on this computer, sends crash reports to a home: off until they say
 * yes, asked once per home after signing in (two equal buttons), changed from the Account
 * menu. Kept in the player's settings, never at the home.
 */
class CrashConsent(private val settings: KeyValueStore) {
    fun answer(home: Home): Boolean? = settings.get(key(home))?.let { it == "on" }
    fun isOn(home: Home): Boolean = answer(home) == true
    fun set(on: Boolean, home: Home) = settings.put(key(home), if (on) "on" else "off")

    companion object {
        fun key(home: Home) = "crashReports." + (home.id ?: home.url)
    }
}

/**
 * The player's crashes: an uncaught exception is kept (its class and frames, never its message,
 * as the Android apps' CrashLog keeps them), the newest five, under the config folder's
 * `crashes/`, and sent to the home at the next sign-in when crash reports are on.
 */
object DesktopCrashes {
    const val OWN = "com.hamzalabs.maktaba"

    fun outbox(config: File) = CrashOutbox(File(config, "crashes"))

    fun header(installId: String, version: String): Envelope.Header = Envelope.Header(
        app = "desktop.player",
        appVersion = version,
        platform = System.getProperty("os.name").orEmpty().lowercase().let { if (it.startsWith("windows")) "windows" else "linux" },
        osVersion = "${System.getProperty("os.name")} ${System.getProperty("os.version")}",
        deviceModel = System.getProperty("os.arch").orEmpty(),
        installId = installId,
    )

    /** A crash as the outbox keeps it: the deepest cause's class and frames (no message). */
    fun kept(e: Throwable, version: String, at: Long = System.currentTimeMillis()): KeptCrash {
        var deepest = e
        val seen = HashSet<Throwable>()
        while (deepest.cause != null && seen.add(deepest)) deepest = deepest.cause!!
        return KeptCrash(
            at = at, type = deepest.javaClass.name,
            frames = deepest.stackTrace.map { "${it.className}.${it.methodName}(${it.fileName}:${it.lineNumber})" },
            appVersion = version, osVersion = "${System.getProperty("os.name")} ${System.getProperty("os.version")}",
            device = System.getProperty("os.arch"),
        )
    }

    /** Keeps uncaught exceptions (once, at start), then lets the previous handler end the app. */
    fun install(config: File, version: String) {
        val outbox = outbox(config)
        val previous = Thread.getDefaultUncaughtExceptionHandler()
        Thread.setDefaultUncaughtExceptionHandler { t, e ->
            runCatching { outbox.keep(kept(e, version)) }
            previous?.uncaughtException(t, e)
        }
    }

    /** A random id made once per install (what the home's limit counts). */
    fun installId(settings: KeyValueStore): String =
        settings.get("installId") ?: UUID.randomUUID().toString().also { settings.put("installId", it) }

    /** Sends what is due when the person turned crash reports on for [home]. */
    suspend fun flush(outbox: CrashOutbox, client: ApiClient, home: Home, consent: CrashConsent, header: Envelope.Header, log: () -> List<String> = { emptyList() }): Int =
        outbox.flush(consent.isOn(home), header, log, Redact::scrub) { env ->
            try {
                client.sendCrashEnvelope(env)
                CrashOutbox.SendResult.Sent
            } catch (e: ApiException.Status) {
                when (e.code) {
                    403 -> CrashOutbox.SendResult.Refused
                    429 -> CrashOutbox.SendResult.Later(e.retryAfterSec?.let { it * 1000 })
                    else -> CrashOutbox.SendResult.Later()
                }
            } catch (e: CancellationException) {
                throw e
            } catch (e: Exception) {
                CrashOutbox.SendResult.Later()
            }
        }
}

/** Opens Report a problem with an error's reference (its request id's first 8), from an error box. */
val LocalReportProblem = androidx.compose.runtime.staticCompositionLocalOf<((String?) -> Unit)?> { null }

/** The reference "Report this" passes: the last request the player made (the call that failed). */
fun lastReference(): String? = RequestId.recent().lastOrNull()?.take(8)

/** The one-time question (53.14 §3): two equal buttons, neither the default. */
@Composable
fun CrashQuestionDialog(home: String, answer: (Boolean) -> Unit) {
    val t = LocalTexts.current
    AlertDialog(
        onDismissRequest = {},
        title = { Text(t["crash.question.title", home]) },
        text = { Text(t["crash.question.body", home]) },
        confirmButton = {
            Row(horizontalArrangement = Arrangement.spacedBy(12.dp)) {
                OutlinedButton(onClick = { answer(true) }) { Text(t["crash.question.yes"]) }
                OutlinedButton(onClick = { answer(false) }) { Text(t["crash.question.no"]) }
            }
        },
    )
}

/**
 * Report a problem (53.15 §2): a category, what happened, when, and what will be sent,
 * previewed. A desktop window cannot be captured without the system's screen recording, so
 * this form offers no screenshot (as the web app's).
 */
@Composable
fun ReportProblemDialog(
    client: ApiClient,
    home: String,
    header: Envelope.Header,
    outbox: CrashOutbox,
    log: () -> List<String> = { emptyList() },
    reference: String? = null,
    onDismiss: () -> Unit,
) {
    val t = LocalTexts.current
    var category by remember { mutableStateOf("other") }
    var text by remember { mutableStateOf("") }
    var earlier by remember { mutableStateOf(false) }
    var withCrashes by remember { mutableStateOf(true) }
    var showLog by remember { mutableStateOf(false) }
    var sending by remember { mutableStateOf(false) }
    var number by remember { mutableStateOf<Int?>(null) }
    var problem by remember { mutableStateOf<String?>(null) }
    val lines = remember { ReportPart.lines(log(), System.currentTimeMillis(), Redact::scrub) }
    val crashCount = remember { outbox.files().size }
    val scope = rememberCoroutineScope()
    AlertDialog(
        onDismissRequest = onDismiss,
        title = { Text(t["report.title"]) },
        text = {
            val n = number
            if (n != null) {
                Text(t["report.sentTo", home, n])
                return@AlertDialog
            }
            Column(Modifier.heightIn(max = 520.dp).verticalScroll(rememberScrollState()), verticalArrangement = Arrangement.spacedBy(8.dp)) {
                Text(t["report.what"], style = MaterialTheme.typography.titleSmall)
                REPORT_CATEGORIES.forEach { c ->
                    Row(Modifier.fillMaxWidth().selectable(selected = category == c, role = Role.RadioButton, onClick = { category = c }),
                        verticalAlignment = Alignment.CenterVertically) {
                        RadioButton(selected = category == c, onClick = null)
                        Text(t["report.category.$c"], Modifier.padding(start = 8.dp))
                    }
                }
                OutlinedTextField(text, { text = it.take(ReportPart.TEXT_MAX) }, label = { Text(t["report.describe"]) }, modifier = Modifier.fillMaxWidth())
                Row(verticalAlignment = Alignment.CenterVertically) {
                    RadioButton(selected = !earlier, onClick = { earlier = false }); Text(t["report.when.now"])
                    RadioButton(selected = earlier, onClick = { earlier = true }); Text(t["report.when.earlier"])
                }
                HorizontalDivider()
                Text(t["report.sent.header"], style = MaterialTheme.typography.titleSmall)
                Text(t["report.sent.device"])
                Text("${header.app} ${header.appVersion} · ${header.osVersion} · ${header.deviceModel}",
                    style = MaterialTheme.typography.bodySmall.copy(textDirection = TextDirection.Ltr), fontFamily = FontFamily.Monospace)
                TextButton(onClick = { showLog = !showLog }) { Text(t["report.sent.log", lines.size]) }
                if (showLog) {
                    SelectionContainer {
                        Column {
                            if (lines.isEmpty()) Text(t["report.noLines"])
                            lines.forEach { Text(it, style = MaterialTheme.typography.labelSmall.copy(textDirection = TextDirection.Ltr), fontFamily = FontFamily.Monospace) }
                        }
                    }
                }
                Text(t["report.requests", minOf(20, RequestId.recent().size)])
                if (crashCount > 0) {
                    Row(verticalAlignment = Alignment.CenterVertically) {
                        Checkbox(withCrashes, { withCrashes = it })
                        Text(t["report.crashes", crashCount])
                    }
                }
                Text(t["report.to.home", home])
                problem?.let { Text(it, color = MaterialTheme.colorScheme.error) }
            }
        },
        confirmButton = {
            if (number != null) {
                TextButton(onClick = onDismiss) { Text(t["report.done"]) }
            } else {
                TextButton(enabled = !sending, onClick = {
                    sending = true
                    problem = null
                    scope.launch {
                        try {
                            val now = System.currentTimeMillis()
                            val part = ReportPart.part(category, text, if (earlier) now - 3_600_000 else null, lines,
                                if (withCrashes) outbox.events(Redact::scrub) else emptyList(), header, RequestId.recent(), reference, false, Redact::scrub)
                            number = client.sendProblemReport(part, null)
                        } catch (e: CancellationException) {
                            throw e
                        } catch (e: ApiException.Status) {
                            problem = if (e.code == 429) t["report.limit"] else t["report.failed"]
                        } catch (e: Exception) {
                            problem = t["report.failed"]
                        } finally {
                            sending = false
                        }
                    }
                }) { Text(t["report.send"]) }
            }
        },
        dismissButton = { if (number == null) TextButton(onClick = onDismiss) { Text(t["report.cancel"]) } },
    )
}

/** My reports (53.15 §5): the person's own, their status and the admin's reply. */
@Composable
fun MyReportsDialog(client: ApiClient, onDismiss: () -> Unit) {
    val t = LocalTexts.current
    var reports by remember { mutableStateOf<List<ReportSummary>?>(null) }
    var failed by remember { mutableStateOf(false) }
    LaunchedEffect(client) {
        try {
            reports = client.problemReports()
        } catch (e: CancellationException) {
            throw e
        } catch (e: Exception) {
            failed = true
        }
    }
    AlertDialog(
        onDismissRequest = onDismiss,
        title = { Text(t["report.mine"]) },
        text = {
            Column(Modifier.heightIn(max = 480.dp).verticalScroll(rememberScrollState()), verticalArrangement = Arrangement.spacedBy(8.dp)) {
                when {
                    failed -> Text(t["report.failed"], color = MaterialTheme.colorScheme.error)
                    reports == null -> Text("…")
                    reports!!.isEmpty() -> Text(t["report.none"])
                }
                reports.orEmpty().forEach { r ->
                    Text("#${r.id} · ${t["report.category.${r.category}"]} · ${t["report.status.${r.status}"]}", style = MaterialTheme.typography.titleSmall)
                    r.reply?.takeIf { it.isNotBlank() }?.let { Text(t["report.reply", it]) }
                    HorizontalDivider()
                }
            }
        },
        confirmButton = { TextButton(onClick = onDismiss) { Text(t["report.done"]) } },
    )
}

/** The words, English and Arabic (the Android apps' and the spec's). */
object DiagnosticsStrings {
    val part = Strings.Part(
        en = mapOf(
            "menu.crashReports" to "Send crash reports to {0}",
            "menu.reportProblem" to "Report a problem…",
            "menu.myReports" to "My reports",
            "report.this" to "Report this",
            "crash.lastCrash.sent" to "The app closed unexpectedly last time. A report was sent to {0}.",
            "crash.lastCrash.kept" to "The app closed unexpectedly last time. The crash is kept on this device; you can attach it to a problem report.",
            "crash.question.title" to "Help {0} fix problems?",
            "crash.question.body" to "If the app crashes, it can send a crash report to {0} so its admin can fix it. It contains no videos or messages. It is off unless you turn it on.",
            "crash.question.yes" to "Send crash reports",
            "crash.question.no" to "Not now",
            "report.title" to "Report a problem",
            "report.what" to "What happened?",
            "report.describe" to "Describe it (optional)",
            "report.category.playback" to "Playback",
            "report.category.live_tv" to "Live TV",
            "report.category.search" to "Search",
            "report.category.signing_in" to "Signing in",
            "report.category.other" to "Something else",
            "report.when.now" to "Just now",
            "report.when.earlier" to "Earlier today",
            "report.sent.header" to "What will be sent",
            "report.sent.device" to "App version, device model and system version",
            "report.sent.log" to "This app's log for the last 30 minutes ({0} lines)",
            "report.noLines" to "No lines in the last 30 minutes.",
            "report.requests" to "The last {0} requests to the home",
            "report.crashes" to "Include recent crashes of this app ({0})",
            "report.to.home" to "Send to the admin of {0}",
            "report.send" to "Send",
            "report.cancel" to "Cancel",
            "report.done" to "Close",
            "report.sentTo" to "Sent to {0} (#{1})",
            "report.limit" to "You've sent many reports today. Try again tomorrow.",
            "report.failed" to "The report could not be sent. Try again.",
            "report.mine" to "My reports",
            "report.none" to "You haven't sent a report.",
            "report.reply" to "Reply: {0}",
            "report.status.new" to "New",
            "report.status.looking" to "Looking into it",
            "report.status.fixed" to "Fixed",
            "report.status.closed" to "Closed",
        ),
        ar = mapOf(
            "menu.crashReports" to "إرسال تقارير الأعطال إلى {0}",
            "menu.reportProblem" to "الإبلاغ عن مشكلة…",
            "menu.myReports" to "بلاغاتي",
            "report.this" to "الإبلاغ عن هذا",
            "crash.lastCrash.sent" to "أُغلق التطبيق بشكل غير متوقع في المرة السابقة. أُرسل تقرير إلى {0}.",
            "crash.lastCrash.kept" to "أُغلق التطبيق بشكل غير متوقع في المرة السابقة. حُفظ العطل على هذا الجهاز، ويمكنك إرفاقه ببلاغ عن مشكلة.",
            "crash.question.title" to "هل تريد مساعدة {0} في إصلاح المشكلات؟",
            "crash.question.body" to "إذا توقف التطبيق فجأة، يمكنه إرسال تقرير عطل إلى {0} ليتمكّن المشرف من إصلاحه. لا يحتوي التقرير على فيديوهات أو رسائل. هذه الميزة متوقفة ما لم تشغّلها.",
            "crash.question.yes" to "إرسال تقارير الأعطال",
            "crash.question.no" to "ليس الآن",
            "report.title" to "الإبلاغ عن مشكلة",
            "report.what" to "ماذا حدث؟",
            "report.describe" to "صِف ما حدث (اختياري)",
            "report.category.playback" to "التشغيل",
            "report.category.live_tv" to "البث المباشر",
            "report.category.search" to "البحث",
            "report.category.signing_in" to "تسجيل الدخول",
            "report.category.other" to "شيء آخر",
            "report.when.now" to "الآن",
            "report.when.earlier" to "في وقت سابق اليوم",
            "report.sent.header" to "ما الذي سيُرسل",
            "report.sent.device" to "إصدار التطبيق وطراز الجهاز وإصدار النظام",
            "report.sent.log" to "سجل هذا التطبيق لآخر 30 دقيقة ({0} سطرًا)",
            "report.noLines" to "لا توجد أسطر في آخر 30 دقيقة.",
            "report.requests" to "آخر {0} طلبًا إلى الجهاز الرئيسي",
            "report.crashes" to "إرفاق الأعطال الأخيرة لهذا التطبيق ({0})",
            "report.to.home" to "الإرسال إلى مشرف {0}",
            "report.send" to "إرسال",
            "report.cancel" to "إلغاء",
            "report.done" to "إغلاق",
            "report.sentTo" to "أُرسل إلى {0} (رقم {1})",
            "report.limit" to "أرسلت بلاغات كثيرة اليوم. حاول مجددًا غدًا.",
            "report.failed" to "تعذّر إرسال البلاغ. حاول مجددًا.",
            "report.mine" to "بلاغاتي",
            "report.none" to "لم ترسل أي بلاغ بعد.",
            "report.reply" to "الرد: {0}",
            "report.status.new" to "جديد",
            "report.status.looking" to "قيد المتابعة",
            "report.status.fixed" to "تم الإصلاح",
            "report.status.closed" to "مغلق",
        ),
    )
}
