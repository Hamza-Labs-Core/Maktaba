package com.hamzalabs.maktaba.core.ui

import android.content.Intent
import android.net.Uri
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.WindowInsets
import androidx.compose.foundation.layout.consumeWindowInsets
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.statusBars
import androidx.compose.foundation.layout.statusBarsPadding
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.semantics.LiveRegionMode
import androidx.compose.ui.semantics.liveRegion
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.unit.dp
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.compose.LocalLifecycleOwner
import androidx.lifecycle.repeatOnLifecycle
import com.hamzalabs.maktaba.core.R
import com.hamzalabs.maktaba.core.api.ApiClient
import com.hamzalabs.maktaba.core.api.CloudStatusRules
import com.hamzalabs.maktaba.core.api.CloudSummary
import com.hamzalabs.maktaba.core.api.HomeReconnect
import com.hamzalabs.maktaba.core.api.HomeStatusDoc
import com.hamzalabs.maktaba.core.api.StatusStripRules
import com.hamzalabs.maktaba.core.api.StripKey
import com.hamzalabs.maktaba.core.api.StripLine
import kotlinx.coroutines.delay
import java.time.Instant
import java.time.ZoneId
import java.time.format.DateTimeFormatter
import java.time.format.FormatStyle

/** How often the home's status is read while the app is in front (story 53.06). */
const val STATUS_POLL_MS = 10_000L

/** A strip line's words (the web's `status.*`, with their Arabic in values-ar). */
fun stripText(key: StripKey): Int = when (key) {
    StripKey.OFFLINE -> R.string.status_offline
    StripKey.RESTARTING -> R.string.status_restarting
    StripKey.DATABASE -> R.string.status_database
    StripKey.SAFE -> R.string.status_safe
    StripKey.STREAMING -> R.string.status_streaming
    StripKey.STREAMING_NO_TV -> R.string.status_streaming_no_tv
    StripKey.SEARCH -> R.string.status_search
    StripKey.PROCESSING -> R.string.status_processing
    StripKey.LOGS -> R.string.status_logs
    StripKey.RELAY_DEGRADED -> R.string.banner_relay_degraded
    StripKey.RELAY_OUTAGE -> R.string.banner_relay_outage
    StripKey.API_DEGRADED -> R.string.banner_api_degraded
    StripKey.API_OUTAGE -> R.string.banner_api_outage
    StripKey.MAINTENANCE -> R.string.banner_maintenance
    StripKey.RECONNECTING -> R.string.home_reconnecting
}

/** A moment's date and time in the device's language and time zone (the maintenance window). */
fun localWhen(ms: Long): Pair<String, String> {
    val t = Instant.ofEpochMilli(ms).atZone(ZoneId.systemDefault())
    return DateTimeFormatter.ofLocalizedDate(FormatStyle.MEDIUM).format(t) to
        DateTimeFormatter.ofLocalizedTime(FormatStyle.SHORT).format(t)
}

/**
 * The status strip above [content] (Epic 53 story 53.06): the home's `/status.json`, read
 * every [intervalMs] while the app is started; the highest line, "N more", Close (it comes back
 * when the state changes) and, for an admin, Details (the home's page in the browser). Nothing
 * while [enabled] is false (a video playing full screen) or all is well.
 */
@Composable
fun HomeStatusHost(
    client: ApiClient,
    admin: Boolean,
    enabled: Boolean = true,
    liveTvOff: Boolean = false,
    intervalMs: Long = STATUS_POLL_MS,
    content: @Composable () -> Unit,
) {
    var status by remember(client) { mutableStateOf<HomeStatusDoc?>(null) }
    var cloud by remember(client) { mutableStateOf<CloudSummary?>(null) }
    var closed by remember(client) { mutableStateOf("") }
    val lifecycle = LocalLifecycleOwner.current.lifecycle
    LaunchedEffect(client) {
        lifecycle.repeatOnLifecycle(Lifecycle.State.STARTED) {
            while (true) {
                client.homeStatus()?.let { status = it }
                delay(intervalMs)
            }
        }
    }
    // Maktaba Cloud's status (story 50.11), only when the home is reached through the cloud
    val viaCloud = CloudStatusRules.throughCloud(client.home)
    LaunchedEffect(client, viaCloud) {
        if (!viaCloud) return@LaunchedEffect
        lifecycle.repeatOnLifecycle(Lifecycle.State.STARTED) {
            while (true) {
                CloudStatusRules.fetch(ApiClient.defaultHttp)?.let { cloud = it }
                delay(CloudStatusRules.POLL_MS)
            }
        }
    }
    val cloudLine = if (viaCloud) CloudStatusRules.line(cloud, System.currentTimeMillis(), ::localWhen) else null
    // a request waiting for the home behind the relay (story 50.13)
    val reconnecting by HomeReconnect.waiting.collectAsState()
    val lines = StatusStripRules.lines(status, online = true, admin = admin, liveTvOff = liveTvOff, cloud = cloudLine, reconnecting = reconnecting > 0)
    val sig = lines.joinToString("|") { "${it.key.name}${it.id ?: ""}${it.args}" }
    val shown = enabled && lines.isNotEmpty() && closed != sig
    Column(Modifier.fillMaxSize()) {
        if (shown) {
            StatusStrip(lines, client.home.toString(), onClose = { closed = sig }, modifier = Modifier.statusBarsPadding())
        }
        Box(Modifier.weight(1f).then(if (shown) Modifier.consumeWindowInsets(WindowInsets.statusBars) else Modifier)) { content() }
    }
}

/** The strip itself: [lines] highest first, Details to [home]'s pages for an admin. */
@Composable
fun StatusStrip(lines: List<StripLine>, home: String, onClose: () -> Unit, modifier: Modifier = Modifier) {
    var open by remember(lines) { mutableStateOf(false) }
    val context = LocalContext.current
    Surface(
        modifier.fillMaxWidth().padding(horizontal = 12.dp, vertical = 6.dp).testTag("status.strip")
            .semantics { liveRegion = LiveRegionMode.Polite },
        color = MaterialTheme.colorScheme.secondaryContainer,
        shape = MaterialTheme.shapes.medium,
    ) {
        Row(Modifier.padding(horizontal = 12.dp, vertical = 8.dp), verticalAlignment = Alignment.CenterVertically) {
            Column(Modifier.weight(1f), verticalArrangement = Arrangement.spacedBy(4.dp)) {
                for (line in if (open) lines else lines.take(1)) {
                    Row(verticalAlignment = Alignment.CenterVertically) {
                        Text(
                            stringResource(stripText(line.key), *line.args.toTypedArray()),
                            Modifier.weight(1f, fill = false).testTag("strip.${line.key.name.lowercase()}"),
                        )
                        (line.link ?: line.details?.let { home.trimEnd('/') + it })?.let { url ->
                            TextButton(onClick = {
                                runCatching {
                                    context.startActivity(Intent(Intent.ACTION_VIEW, Uri.parse(url)).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK))
                                }
                            }) { Text(stringResource(R.string.status_details)) }
                        }
                    }
                }
                if (lines.size > 1 && !open) {
                    TextButton(onClick = { open = true }) { Text(stringResource(R.string.status_more, lines.size - 1)) }
                }
            }
            TextButton(onClick = onClose) { Text(stringResource(R.string.status_close)) }
        }
    }
}
