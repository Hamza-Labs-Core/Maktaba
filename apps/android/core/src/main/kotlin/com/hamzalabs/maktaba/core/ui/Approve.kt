package com.hamzalabs.maktaba.core.ui

import androidx.activity.compose.BackHandler
import androidx.compose.foundation.border
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.imePadding
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.text.KeyboardActions
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.CheckCircle
import androidx.compose.material.icons.filled.Close
import androidx.compose.material.icons.outlined.Devices
import androidx.compose.material.icons.outlined.QrCodeScanner
import androidx.compose.material.icons.outlined.WarningAmber
import androidx.compose.material3.Button
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Text
import androidx.compose.material3.TopAppBar
import androidx.compose.runtime.Composable
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalLayoutDirection
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.semantics.LiveRegionMode
import androidx.compose.ui.semantics.liveRegion
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.input.ImeAction
import androidx.compose.ui.text.input.KeyboardCapitalization
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.unit.LayoutDirection
import androidx.compose.ui.unit.dp
import com.hamzalabs.maktaba.core.R
import com.hamzalabs.maktaba.core.api.ApiClient
import com.hamzalabs.maktaba.core.api.ApiException
import com.hamzalabs.maktaba.core.api.PairingCode
import com.hamzalabs.maktaba.core.api.PairingLookup
import com.hamzalabs.maktaba.core.api.PairingRequest
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.launch

/** Where approving a sign-in stands. */
private sealed interface ApproveStep {
    data object Enter : ApproveStep
    data object Looking : ApproveStep

    /** The code and what asks with it (null: the home cannot say). */
    data class Confirm(val code: String, val request: PairingRequest?) : ApproveStep
    data class Done(val who: String) : ApproveStep
}

/**
 * "Approve a sign-in" (story 42.6): another device shows a code (and a QR of the home's
 * approve page); a person signed in here types or scans it, sees what is asking (the app, the
 * platform, the device's name), and approves it explicitly. That device is then signed in as
 * this person. The home checks every rule (a sign-in through the cloud cannot approve).
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun ApproveSignIn(client: ApiClient, username: String, homeName: String, onClose: () -> Unit, onSignedOut: () -> Unit) {
    var step by remember { mutableStateOf<ApproveStep>(ApproveStep.Enter) }
    var text by remember { mutableStateOf("") }
    var problem by remember { mutableStateOf<String?>(null) }
    var busy by remember { mutableStateOf(false) }
    var scanning by remember { mutableStateOf(false) }
    val scope = rememberCoroutineScope()
    val invalid = stringResource(R.string.approve_code_invalid)
    val notCode = stringResource(R.string.not_a_code)
    val unknown = stringResource(R.string.approve_unknown, homeName)
    val already = stringResource(R.string.approve_already)
    val cloud = stringResource(R.string.approve_cloud)
    val unreachable = stringResource(R.string.signin_unreachable, homeName)
    val someDevice = stringResource(R.string.approve_unknown_device)

    fun failed(e: Exception): String = when (e) {
        is ApiException.Transport -> unreachable
        is ApiException.Status -> when (e.code) {
            409 -> already
            403 -> cloud
            404, 410, 422 -> unknown
            else -> e.message ?: e.toString()
        }
        else -> e.message ?: e.toString()
    }

    fun look(raw: String) {
        val code = PairingCode.normalize(raw) ?: run { problem = invalid; return }
        problem = null
        step = ApproveStep.Looking
        scope.launch {
            try {
                step = when (val found = client.pairingRequest(code)) {
                    is PairingLookup.Waiting -> ApproveStep.Confirm(code, found.request)
                    PairingLookup.Unsupported -> ApproveStep.Confirm(code, null)
                    PairingLookup.Unknown -> { problem = unknown; ApproveStep.Enter }
                }
            } catch (e: CancellationException) {
                throw e
            } catch (e: ApiException.SignedOut) {
                onSignedOut()
            } catch (e: Exception) {
                problem = failed(e)
                step = ApproveStep.Enter
            }
        }
    }

    fun approve(confirm: ApproveStep.Confirm) {
        busy = true
        problem = null
        scope.launch {
            try {
                client.approvePairing(confirm.code)
                step = ApproveStep.Done(confirm.request?.deviceLabel?.takeIf { it.isNotBlank() } ?: someDevice)
            } catch (e: CancellationException) {
                throw e
            } catch (e: ApiException.SignedOut) {
                onSignedOut()
            } catch (e: Exception) {
                problem = failed(e)
            } finally {
                busy = false
            }
        }
    }

    if (scanning) {
        BackHandler { scanning = false }
        QrScanner(
            title = stringResource(R.string.approve_scan_title),
            hint = stringResource(R.string.approve_scan_hint),
            onScan = { scanned ->
                scanning = false
                val code = PairingCode.fromScan(scanned)
                if (code == null) problem = notCode else { text = PairingCode.shown(code); look(code) }
            },
            onClose = { scanning = false },
        )
        return
    }

    BackHandler(onBack = onClose)
    Scaffold(
        topBar = {
            TopAppBar(
                title = { Text(stringResource(R.string.approve_title)) },
                navigationIcon = { IconButton(onClick = onClose) { Icon(Icons.Filled.Close, stringResource(R.string.close)) } },
            )
        },
    ) { padding ->
        Box(Modifier.padding(padding).fillMaxSize().imePadding().verticalScroll(rememberScrollState()), contentAlignment = Alignment.TopCenter) {
            Column(
                Modifier.widthIn(max = 560.dp).fillMaxWidth().padding(20.dp),
                verticalArrangement = Arrangement.spacedBy(14.dp),
            ) {
                when (val s = step) {
                    ApproveStep.Enter, ApproveStep.Looking -> {
                        Text(stringResource(R.string.approve_intro), style = MaterialTheme.typography.bodyLarge)
                        CompositionLocalProvider(LocalLayoutDirection provides LayoutDirection.Ltr) {
                            OutlinedTextField(
                                value = text,
                                onValueChange = { text = it.take(16); problem = null },
                                label = { Text(stringResource(R.string.approve_code_label)) },
                                singleLine = true,
                                enabled = s == ApproveStep.Enter,
                                textStyle = MaterialTheme.typography.titleLarge.copy(fontFamily = FontFamily.Monospace),
                                keyboardOptions = KeyboardOptions(capitalization = KeyboardCapitalization.Characters, autoCorrectEnabled = false, imeAction = ImeAction.Go),
                                keyboardActions = KeyboardActions(onGo = { look(text) }),
                                modifier = Modifier.fillMaxWidth().testTag("approve-code"),
                            )
                        }
                        Button(
                            onClick = { look(text) },
                            enabled = text.isNotBlank() && s == ApproveStep.Enter,
                            modifier = Modifier.fillMaxWidth().testTag("approve-look"),
                        ) {
                            if (s == ApproveStep.Looking) CircularProgressIndicator(Modifier.size(18.dp), strokeWidth = 2.dp) else Text(stringResource(R.string.approve_look))
                        }
                        OutlinedButton(onClick = { problem = null; scanning = true }, enabled = s == ApproveStep.Enter, modifier = Modifier.fillMaxWidth()) {
                            Icon(Icons.Outlined.QrCodeScanner, null, Modifier.size(18.dp))
                            Spacer(Modifier.width(8.dp))
                            Text(stringResource(R.string.scan_qr))
                        }
                    }
                    is ApproveStep.Confirm -> ApproveCard(
                        code = s.code,
                        request = s.request,
                        username = username,
                        homeName = homeName,
                        busy = busy,
                        onApprove = { approve(s) },
                        onCancel = { step = ApproveStep.Enter; problem = null },
                    )
                    is ApproveStep.Done -> {
                        Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(12.dp)) {
                            Icon(Icons.Filled.CheckCircle, null, tint = MaterialTheme.colorScheme.primary)
                            Text(
                                stringResource(R.string.approve_done, s.who),
                                style = MaterialTheme.typography.titleMedium,
                                modifier = Modifier.semantics { liveRegion = LiveRegionMode.Polite }.testTag("approve-done"),
                            )
                        }
                        Button(onClick = onClose, modifier = Modifier.fillMaxWidth()) { Text(stringResource(R.string.close)) }
                        OutlinedButton(onClick = { text = ""; step = ApproveStep.Enter }, modifier = Modifier.fillMaxWidth()) {
                            Text(stringResource(R.string.approve_another))
                        }
                    }
                }
                problem?.let {
                    Text(
                        it,
                        color = MaterialTheme.colorScheme.error,
                        style = MaterialTheme.typography.bodyMedium,
                        modifier = Modifier.semantics { liveRegion = LiveRegionMode.Polite }.testTag("approve-problem"),
                    )
                }
            }
        }
    }
}

/**
 * What is asking to sign in with [code]: the device's name, the app and platform, the code,
 * when it runs out, what approving means, and an explicit Approve. [request] null: the home is
 * too old to say, and the card says to approve only a code seen on one's own device.
 */
@Composable
fun ApproveCard(
    code: String,
    request: PairingRequest?,
    username: String,
    homeName: String,
    busy: Boolean,
    onApprove: () -> Unit,
    onCancel: () -> Unit,
    now: () -> Long = System::currentTimeMillis,
) {
    val colors = MaterialTheme.colorScheme
    Column(
        Modifier.fillMaxWidth().border(1.dp, colors.outlineVariant, RoundedCornerShape(16.dp)).padding(16.dp).testTag("approve-card"),
        verticalArrangement = Arrangement.spacedBy(10.dp),
    ) {
        Text(stringResource(R.string.approve_asking), style = MaterialTheme.typography.labelLarge, color = colors.primary)
        Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(12.dp)) {
            Icon(Icons.Outlined.Devices, null, tint = colors.primary)
            Column(Modifier.weight(1f)) {
                Text(
                    request?.deviceLabel?.takeIf { it.isNotBlank() } ?: stringResource(R.string.approve_unknown_device),
                    style = MaterialTheme.typography.titleLarge,
                )
                request?.let { r -> describe(r).takeIf { it.isNotEmpty() } }?.let {
                    Text(it, style = MaterialTheme.typography.bodyMedium, color = colors.onSurfaceVariant)
                }
            }
        }
        CompositionLocalProvider(LocalLayoutDirection provides LayoutDirection.Ltr) {
            Text(
                stringResource(R.string.approve_code_line, PairingCode.shown(code)),
                style = MaterialTheme.typography.titleMedium.copy(fontFamily = FontFamily.Monospace),
            )
        }
        request?.expiresMillis?.let { exp ->
            val minutes = ((exp - now()) / 60_000L).coerceAtLeast(0L).toInt() + 1
            Text(stringResource(R.string.approve_expires, minutes), style = MaterialTheme.typography.bodySmall, color = colors.onSurfaceVariant)
        }
        Row(horizontalArrangement = Arrangement.spacedBy(8.dp), verticalAlignment = Alignment.Top) {
            Icon(Icons.Outlined.WarningAmber, null, tint = colors.error, modifier = Modifier.size(20.dp))
            Text(
                if (request == null) stringResource(R.string.approve_unsupported, homeName) else stringResource(R.string.approve_warning, username),
                style = MaterialTheme.typography.bodyMedium,
                textAlign = TextAlign.Start,
            )
        }
        Row(horizontalArrangement = Arrangement.spacedBy(12.dp), modifier = Modifier.fillMaxWidth()) {
            OutlinedButton(onClick = onCancel, enabled = !busy, modifier = Modifier.weight(1f)) { Text(stringResource(R.string.cancel)) }
            Button(onClick = onApprove, enabled = !busy, modifier = Modifier.weight(1f).testTag("approve-button")) {
                if (busy) CircularProgressIndicator(Modifier.size(18.dp), strokeWidth = 2.dp) else Text(stringResource(R.string.approve_button))
            }
        }
    }
}

/** "Player · android · phone": what the home says the asking app is. */
internal fun describe(r: PairingRequest): String =
    listOfNotNull(r.clientApp, r.platform, r.device)
        .map { it.trim() }
        .filter { it.isNotEmpty() }
        .joinToString(" · ") { it.replaceFirstChar { c -> c.titlecase() } }
