package com.hamzalabs.maktaba.desktop

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.widthIn
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Button
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.unit.dp
import com.hamzalabs.maktaba.core.api.ApiClient
import com.hamzalabs.maktaba.core.api.ApiException
import com.hamzalabs.maktaba.core.api.PairingCode
import com.hamzalabs.maktaba.core.api.PairingLookup
import com.hamzalabs.maktaba.core.api.PairingRequest
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.launch
import java.io.File
import java.time.Instant
import java.time.ZoneId
import java.time.format.DateTimeFormatter
import java.time.format.FormatStyle
import java.util.Locale

/**
 * Approve a sign-in (story 42.6): type the code another device shows, see what is asking,
 * then an explicit Approve. The device gets this person's own account and rights; the home
 * checks every rule (a sign-in through Maktaba Cloud cannot approve: 403).
 */
@Composable
fun ApproveDialog(client: ApiClient, onDismiss: () -> Unit) {
    val t = LocalTexts.current
    val scope = rememberCoroutineScope()
    var code by remember { mutableStateOf("") }
    var asking by remember { mutableStateOf<PairingRequest?>(null) }
    var older by remember { mutableStateOf(false) }
    var busy by remember { mutableStateOf(false) }
    var done by remember { mutableStateOf(false) }
    var problem by remember { mutableStateOf<String?>(null) }

    fun failure(e: Exception): String = when ((e as? ApiException.Status)?.code) {
        409 -> t["approve.used"]
        403 -> t["approve.cloud"]
        404, 410, 422 -> t["approve.notWaiting"]
        else -> e.message ?: t["common.error"]
    }

    fun lookUp() {
        if (PairingCode.normalize(code) == null) {
            problem = t["approve.checkCode"]
            return
        }
        busy = true
        problem = null
        scope.launch {
            try {
                when (val r = client.pairingRequest(code)) {
                    is PairingLookup.Waiting -> asking = r.request
                    PairingLookup.Unknown -> problem = t["approve.notWaiting"]
                    PairingLookup.Unsupported -> older = true
                }
            } catch (e: CancellationException) {
                throw e
            } catch (e: Exception) {
                problem = failure(e)
            } finally {
                busy = false
            }
        }
    }

    fun approve() {
        busy = true
        problem = null
        scope.launch {
            try {
                client.approvePairing(code)
                done = true
            } catch (e: CancellationException) {
                throw e
            } catch (e: Exception) {
                problem = failure(e)
            } finally {
                busy = false
            }
        }
    }

    val ready = asking != null || older
    AlertDialog(
        onDismissRequest = { if (!busy) onDismiss() },
        title = { Text(t["approve.title"]) },
        text = {
            Column(Modifier.widthIn(min = 360.dp), verticalArrangement = Arrangement.spacedBy(12.dp)) {
                when {
                    done -> Text(t["approve.done"])
                    ready -> {
                        Text("⁦${PairingCode.shown(code)}⁩")
                        asking?.let { r ->
                            Text(t["approve.asking", r.clientApp.orEmpty().ifEmpty { "?" }, r.platform.orEmpty().ifEmpty { "?" }, r.device.orEmpty().ifEmpty { "?" }])
                            r.deviceLabel?.takeIf { it.isNotBlank() }?.let { Text(t["approve.label", it]) }
                            r.expiresMillis?.let { Text(t["approve.expires", formatTime(it, t.lang)]) }
                        }
                        if (older) Text(t["approve.older"])
                    }
                    else -> {
                        Text(t["approve.hint"])
                        OutlinedTextField(code, { code = it; problem = null }, label = { Text(t["approve.code"]) }, singleLine = true)
                    }
                }
                if (busy) CircularProgressIndicator()
                problem?.let { Problem(it) }
            }
        },
        confirmButton = {
            when {
                done -> Button(onClick = onDismiss) { Text(t["common.close"]) }
                ready -> Button(onClick = ::approve, enabled = !busy) { Text(t["approve.approve"]) }
                else -> Button(onClick = ::lookUp, enabled = !busy && code.isNotBlank()) { Text(t["approve.continue"]) }
            }
        },
        dismissButton = { if (!done) TextButton(onClick = onDismiss, enabled = !busy) { Text(t["common.cancel"]) } },
    )
}

private fun formatTime(millis: Long, lang: Lang): String =
    DateTimeFormatter.ofLocalizedTime(FormatStyle.SHORT).withLocale(Locale.forLanguageTag(lang.code))
        .format(Instant.ofEpochMilli(millis).atZone(ZoneId.systemDefault()))

/**
 * Check for updates: the players' feed, the version this computer would install, and on a yes
 * the download, checked against the apps' key before anything runs.
 */
@Composable
fun UpdateDialog(onDismiss: () -> Unit, onInstalling: () -> Unit) {
    val t = LocalTexts.current
    val scope = rememberCoroutineScope()
    var state by remember { mutableStateOf(t["update.checking"]) }
    var offer by remember { mutableStateOf<Pair<PlayerFeed, FeedEntry>?>(null) }
    var busy by remember { mutableStateOf(true) }
    LaunchedEffect(Unit) {
        try {
            val feed = Updates.fetch()
            val entry = Updates.entryFor(feed)
            if (entry != null && Updates.isNewer(feed.version, BuildInfo.VERSION)) {
                offer = feed to entry
                state = t["update.available", feed.version, BuildInfo.VERSION]
            } else {
                state = t["update.upToDate", BuildInfo.VERSION]
            }
        } catch (e: CancellationException) {
            throw e
        } catch (e: Exception) {
            state = t["update.failed", e.message ?: e.toString()]
        } finally {
            busy = false
        }
    }
    val o = offer
    AlertDialog(
        onDismissRequest = { if (!busy) onDismiss() },
        title = { Text(t["update.title"]) },
        text = {
            Column(verticalArrangement = Arrangement.spacedBy(12.dp)) {
                Text(state)
                if (o != null && BuildInfo.UPDATER_PUBKEY.isEmpty()) Text(t["update.unsigned"])
                if (busy) CircularProgressIndicator()
            }
        },
        confirmButton = {
            if (o != null && BuildInfo.UPDATER_PUBKEY.isNotEmpty() && !busy) {
                Button(onClick = {
                    busy = true
                    state = t["update.downloading"]
                    scope.launch {
                        try {
                            val file = Updates.download(o.second, o.first.version, BuildInfo.UPDATER_PUBKEY, File(AppDirs.config, "updates"))
                            if (Updates.install(file) != null) {
                                state = t["update.installing"]
                                onInstalling()
                            } else {
                                state = t["update.linux", file.absolutePath]
                                offer = null
                            }
                        } catch (e: CancellationException) {
                            throw e
                        } catch (e: Exception) {
                            state = t["update.failed", e.message ?: e.toString()]
                            offer = null
                        } finally {
                            busy = false
                        }
                    }
                }) { Text(t["update.install"]) }
            } else {
                Button(onClick = onDismiss, enabled = !busy) { Text(t["common.close"]) }
            }
        },
        dismissButton = { if (o != null && !busy) TextButton(onClick = onDismiss) { Text(t["common.cancel"]) } },
    )
}
