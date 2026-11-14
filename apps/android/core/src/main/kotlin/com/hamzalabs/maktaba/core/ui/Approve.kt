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
import androidx.compose.material.icons.outlined.ConnectedTv
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
import androidx.compose.ui.platform.LocalLocale
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
import androidx.compose.ui.platform.LocalResources
import com.hamzalabs.maktaba.core.R
import com.hamzalabs.maktaba.core.api.ApiClient
import com.hamzalabs.maktaba.core.api.CloudTvCode
import com.hamzalabs.maktaba.core.api.PairingCode
import com.hamzalabs.maktaba.core.api.PairingRequest
import kotlinx.coroutines.launch

/**
 * "Approve a sign-in" (story 42.6): another device shows a code (and a QR of the home's
 * approve page); a person signed in here types or scans it, sees what is asking (the app, the
 * platform, the device's name), and approves it explicitly. That device is then signed in as
 * this person. The home checks every rule (a sign-in through the cloud cannot approve). A TV's
 * Maktaba Cloud code (story 58.03) is approved here too, once the person confirms the TV: the
 * steps are [ApproveCode]'s.
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun ApproveSignIn(client: ApiClient, username: String, homeName: String, onClose: () -> Unit, onSignedOut: () -> Unit) {
    val flow = remember(client) { ApproveCode(client, onSignedOut) }
    val step = flow.step
    val busy = flow.busy
    var text by remember { mutableStateOf("") }
    var scanning by remember { mutableStateOf(false) }
    val scope = rememberCoroutineScope()
    val resources = LocalResources.current
    val problem = flow.problem?.let { p ->
        when (p) {
            ApproveProblem.Invalid -> stringResource(R.string.approve_code_invalid)
            ApproveProblem.NotACode -> stringResource(R.string.not_a_code)
            ApproveProblem.Unknown -> stringResource(R.string.approve_unknown)
            ApproveProblem.Already -> stringResource(R.string.approve_already)
            ApproveProblem.FromCloud -> stringResource(R.string.approve_cloud)
            ApproveProblem.NeedsRemote -> stringResource(R.string.approve_cloud_needs_remote)
            ApproveProblem.Unreachable -> stringResource(R.string.signin_unreachable, homeName)
            is ApproveProblem.Other -> resources.getString(p.kind.sentence())
        }
    }

    fun look(raw: String) {
        scope.launch { flow.lookUp(raw) }
    }

    fun approve() {
        scope.launch { flow.approve() }
    }

    if (scanning) {
        BackHandler { scanning = false }
        QrScanner(
            title = stringResource(R.string.approve_scan_title),
            hint = stringResource(R.string.approve_scan_hint),
            onScan = { scanned ->
                scanning = false
                val code = PairingCode.fromScan(scanned)
                if (code == null) flow.problem = ApproveProblem.NotACode else { text = PairingCode.shown(code); look(code) }
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
                                onValueChange = { text = it.take(16); flow.problem = null },
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
                        OutlinedButton(onClick = { flow.problem = null; scanning = true }, enabled = s == ApproveStep.Enter, modifier = Modifier.fillMaxWidth()) {
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
                        onApprove = ::approve,
                        onCancel = flow::reset,
                    )
                    is ApproveStep.CloudConfirm -> CloudApproveCard(
                        code = s.code,
                        tv = s.tv,
                        username = username,
                        busy = busy,
                        onApprove = ::approve,
                        onCancel = flow::reset,
                    )
                    is ApproveStep.Done, ApproveStep.CloudDone -> {
                        Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(12.dp)) {
                            Icon(Icons.Filled.CheckCircle, null, tint = MaterialTheme.colorScheme.primary)
                            Text(
                                if (s is ApproveStep.Done) {
                                    stringResource(R.string.approve_done, s.who ?: stringResource(R.string.approve_unknown_device))
                                } else {
                                    stringResource(R.string.approve_cloud_done)
                                },
                                style = MaterialTheme.typography.titleMedium,
                                modifier = Modifier.semantics { liveRegion = LiveRegionMode.Polite }.testTag("approve-done"),
                            )
                        }
                        Button(onClick = onClose, modifier = Modifier.fillMaxWidth()) { Text(stringResource(R.string.close)) }
                        OutlinedButton(onClick = { text = ""; flow.reset() }, modifier = Modifier.fillMaxWidth()) {
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
        request?.deviceLabel?.takeIf { it.isNotBlank() }?.let { name ->
            // the question Approve answers: this device, as this person
            Text(
                stringResource(R.string.approve_confirm, name, username),
                style = MaterialTheme.typography.titleMedium,
                modifier = Modifier.testTag("approve-confirm"),
            )
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

/**
 * A TV asking with a Maktaba Cloud code (story 58.03): "Is this your TV? Samsung, in Jordan." (the
 * platform alone when the cloud did not say where), the code, when it runs out, what approving
 * means, and an explicit Approve.
 */
@Composable
fun CloudApproveCard(
    code: String,
    tv: CloudTvCode,
    username: String,
    busy: Boolean,
    onApprove: () -> Unit,
    onCancel: () -> Unit,
    now: () -> Long = System::currentTimeMillis,
) {
    val colors = MaterialTheme.colorScheme
    val locale = LocalLocale.current.platformLocale
    val platform = ApproveCode.platformName(tv) ?: stringResource(R.string.approve_cloud_a_tv)
    val country = ApproveCode.countryName(tv, locale)
    Column(
        Modifier.fillMaxWidth().border(1.dp, colors.outlineVariant, RoundedCornerShape(16.dp)).padding(16.dp).testTag("approve-cloud-card"),
        verticalArrangement = Arrangement.spacedBy(10.dp),
    ) {
        Text(stringResource(R.string.approve_asking), style = MaterialTheme.typography.labelLarge, color = colors.primary)
        Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(12.dp)) {
            Icon(Icons.Outlined.ConnectedTv, null, tint = colors.primary)
            Text(
                if (country != null) {
                    stringResource(R.string.approve_cloud_confirm, platform, country)
                } else {
                    stringResource(R.string.approve_cloud_confirm_no_country, platform)
                },
                style = MaterialTheme.typography.titleLarge,
                modifier = Modifier.weight(1f).testTag("approve-cloud-confirm"),
            )
        }
        CompositionLocalProvider(LocalLayoutDirection provides LayoutDirection.Ltr) {
            Text(
                stringResource(R.string.approve_code_line, PairingCode.shown(code)),
                style = MaterialTheme.typography.titleMedium.copy(fontFamily = FontFamily.Monospace),
            )
        }
        tv.expiresMillis?.let { exp ->
            val minutes = ((exp - now()) / 60_000L).coerceAtLeast(0L).toInt() + 1
            Text(stringResource(R.string.approve_expires, minutes), style = MaterialTheme.typography.bodySmall, color = colors.onSurfaceVariant)
        }
        Row(horizontalArrangement = Arrangement.spacedBy(8.dp), verticalAlignment = Alignment.Top) {
            Icon(Icons.Outlined.WarningAmber, null, tint = colors.error, modifier = Modifier.size(20.dp))
            Text(stringResource(R.string.approve_warning, username), style = MaterialTheme.typography.bodyMedium, textAlign = TextAlign.Start)
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
