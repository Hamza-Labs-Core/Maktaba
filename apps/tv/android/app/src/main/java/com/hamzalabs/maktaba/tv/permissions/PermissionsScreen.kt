package com.hamzalabs.maktaba.tv.permissions

import android.app.Activity
import android.content.Context
import android.content.ContextWrapper
import android.content.Intent
import android.content.pm.PackageManager
import android.net.Uri
import android.os.Build
import android.provider.Settings
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.border
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.focus.FocusRequester
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.LocalLifecycleOwner
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.semantics.heading
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.unit.dp
import androidx.compose.ui.window.Dialog
import androidx.compose.ui.window.DialogProperties
import androidx.core.content.ContextCompat
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.LifecycleEventObserver
import androidx.tv.material3.ExperimentalTvMaterial3Api
import androidx.tv.material3.MaterialTheme
import androidx.tv.material3.Text
import com.hamzalabs.maktaba.core.permissions.AppPermission
import com.hamzalabs.maktaba.core.permissions.AppPermissions
import com.hamzalabs.maktaba.core.permissions.Need
import com.hamzalabs.maktaba.core.permissions.PermissionAction
import com.hamzalabs.maktaba.core.permissions.PermissionMemory
import com.hamzalabs.maktaba.core.permissions.PermissionRow
import com.hamzalabs.maktaba.core.permissions.PermissionState
import com.hamzalabs.maktaba.core.permissions.Walkthrough
import com.hamzalabs.maktaba.core.permissions.action
import com.hamzalabs.maktaba.core.permissions.ok
import com.hamzalabs.maktaba.core.permissions.permissionState
import com.hamzalabs.maktaba.core.session.KeyValueStore
import com.hamzalabs.maktaba.tv.R
import com.hamzalabs.maktaba.tv.ui.components.LibraryBackdrop
import com.hamzalabs.maktaba.tv.ui.components.MaktabaButton
import com.hamzalabs.maktaba.tv.ui.components.ReadingPanel
import com.hamzalabs.maktaba.tv.ui.theme.MaktabaPalette

private const val PREFS = "permissions"

private fun Context.activity(): Activity? {
    var c: Context? = this
    while (c is ContextWrapper) {
        if (c is Activity) return c
        c = c.baseContext
    }
    return null
}

private fun prefs(context: Context) = context.applicationContext.getSharedPreferences(PREFS, Context.MODE_PRIVATE).let { sp ->
    object : KeyValueStore {
        override fun get(key: String): String? = sp.getString(key, null)
        override fun put(key: String, value: String?) {
            sp.edit().apply { if (value == null) remove(key) else putString(key, value) }.apply()
        }
    }
}

private fun read(context: Context, memory: PermissionMemory, permissions: List<AppPermission>) = permissions.map { p ->
    val sdk = Build.VERSION.SDK_INT
    val granted = sdk < p.sinceSdk || ContextCompat.checkSelfPermission(context, p.permission) == PackageManager.PERMISSION_GRANTED
    val rationale = sdk >= p.sinceSdk && context.activity()?.shouldShowRequestPermissionRationale(p.permission) == true
    PermissionRow(p, permissionState(sdk, p, granted, memory.asked(p), rationale))
}

/**
 * The TV app behind its permissions walkthrough: shown on the first run and again whenever a
 * required permission is missing, read again each time the app resumes (back from Android's
 * dialog, from Settings, or to the foreground). «Not now» (or Back) puts it away until the
 * app next comes to the foreground. It is a dialog, so the D-pad stays in it while it shows.
 */
@Composable
fun TvPermissionsGate(permissions: List<AppPermission> = AppPermissions.tvApp, content: @Composable () -> Unit) {
    val context = LocalContext.current
    val memory = remember { PermissionMemory(prefs(context)) }
    var rows by remember { mutableStateOf(read(context, memory, permissions)) }
    var seen by remember { mutableStateOf(memory.seen) }
    var dismissed by rememberSaveable { mutableStateOf(false) }
    val owner = LocalLifecycleOwner.current
    DisposableEffect(owner) {
        val observer = LifecycleEventObserver { _, event ->
            when (event) {
                Lifecycle.Event.ON_START -> dismissed = false
                Lifecycle.Event.ON_RESUME -> rows = read(context, memory, permissions)
                else -> {}
            }
        }
        owner.lifecycle.addObserver(observer)
        onDispose { owner.lifecycle.removeObserver(observer) }
    }
    val launcher = rememberLauncherForActivityResult(ActivityResultContracts.RequestPermission()) {
        rows = read(context, memory, permissions)
    }
    content()
    if (Walkthrough.shouldShow(seen, rows, dismissed)) {
        val done = {
            memory.seen = true
            seen = true
            dismissed = true
        }
        Dialog(
            onDismissRequest = done,
            properties = DialogProperties(usePlatformDefaultWidth = false, dismissOnClickOutside = false),
        ) {
            TvPermissionsScreen(
                rows = Walkthrough.shown(rows),
                onAction = { p ->
                    when (rows.first { it.permission == p }.state.action) {
                        PermissionAction.Ask -> {
                            memory.markAsked(p)
                            launcher.launch(p.permission)
                        }
                        PermissionAction.OpenSettings -> runCatching {
                            context.startActivity(
                                Intent(Settings.ACTION_APPLICATION_DETAILS_SETTINGS, Uri.fromParts("package", context.packageName, null)),
                            )
                        }
                        PermissionAction.None -> {}
                    }
                },
                onContinue = done,
            )
        }
    }
}

/**
 * The walkthrough at ten feet: what each permission is for, where it stands, a button that
 * asks (or opens Settings once Android will not ask again), then Continue. The first button
 * that needs pressing has the focus.
 */
@OptIn(ExperimentalTvMaterial3Api::class)
@Composable
fun TvPermissionsScreen(rows: List<PermissionRow>, onAction: (AppPermission) -> Unit, onContinue: () -> Unit) {
    val first = remember { FocusRequester() }
    val firstAction = rows.firstOrNull { it.state.action != PermissionAction.None }?.permission
    LaunchedEffect(firstAction) { runCatching { first.requestFocus() } }
    LibraryBackdrop(Modifier.testTag("tv-permissions")) {
        Box(Modifier.fillMaxSize().padding(horizontal = 48.dp, vertical = 27.dp), contentAlignment = Alignment.Center) {
            ReadingPanel(Modifier.widthIn(max = 880.dp), contentPadding = 32.dp) {
                Column(verticalArrangement = Arrangement.spacedBy(16.dp)) {
                    Text(
                        stringResource(R.string.tv_perm_title),
                        style = MaterialTheme.typography.headlineMedium,
                        color = MaktabaPalette.Text,
                        modifier = Modifier.semantics { heading() },
                    )
                    Text(stringResource(R.string.tv_perm_intro), style = MaterialTheme.typography.bodyLarge, color = MaktabaPalette.Muted)
                    rows.forEach { row ->
                        Row(
                            Modifier.fillMaxWidth()
                                .border(1.dp, MaktabaPalette.Border, RoundedCornerShape(12.dp))
                                .padding(20.dp)
                                .testTag("tv-permission-${row.permission.id}"),
                            horizontalArrangement = Arrangement.spacedBy(24.dp),
                            verticalAlignment = Alignment.CenterVertically,
                        ) {
                            Column(Modifier.weight(1f), verticalArrangement = Arrangement.spacedBy(6.dp)) {
                                Row(horizontalArrangement = Arrangement.spacedBy(12.dp), verticalAlignment = Alignment.CenterVertically) {
                                    Text(stringResource(titleOf(row.permission)), style = MaterialTheme.typography.titleLarge, color = MaktabaPalette.Text)
                                    Text(
                                        stringResource(if (row.permission.need == Need.Required) R.string.tv_perm_required else R.string.tv_perm_optional),
                                        style = MaterialTheme.typography.labelMedium,
                                        color = MaktabaPalette.Gold,
                                        modifier = Modifier.border(1.dp, MaktabaPalette.Gold, RoundedCornerShape(50)).padding(horizontal = 10.dp, vertical = 2.dp),
                                    )
                                }
                                Text(stringResource(whyOf(row.permission)), style = MaterialTheme.typography.bodyMedium, color = MaktabaPalette.Text)
                                val refused = row.state == PermissionState.Denied || row.state == PermissionState.DeniedPermanently
                                Text(
                                    stringResource(stateText(row.state)),
                                    style = MaterialTheme.typography.titleSmall,
                                    color = if (row.state.ok) MaktabaPalette.Live else if (refused) MaktabaPalette.GoldLight else MaktabaPalette.Muted,
                                    modifier = Modifier.testTag("tv-permission-${row.permission.id}-state"),
                                )
                                if (refused) Text(stringResource(withoutOf(row.permission)), style = MaterialTheme.typography.bodySmall, color = MaktabaPalette.Muted)
                                if (row.state == PermissionState.DeniedPermanently) {
                                    Text(stringResource(R.string.tv_perm_settings_hint), style = MaterialTheme.typography.bodySmall, color = MaktabaPalette.Muted)
                                }
                            }
                            val action = row.state.action
                            if (action != PermissionAction.None) {
                                val focus = if (row.permission == firstAction) Modifier.focusRequester(first) else Modifier
                                MaktabaButton(
                                    onClick = { onAction(row.permission) },
                                    modifier = focus.testTag("tv-permission-${row.permission.id}-${if (action == PermissionAction.Ask) "ask" else "settings"}"),
                                ) {
                                    Text(stringResource(if (action == PermissionAction.Ask) R.string.tv_perm_allow else R.string.tv_perm_open_settings))
                                }
                            }
                        }
                    }
                    val allOk = rows.all { it.state.ok }
                    Row(horizontalArrangement = Arrangement.spacedBy(24.dp), verticalAlignment = Alignment.CenterVertically) {
                        val focus = if (firstAction == null) Modifier.focusRequester(first) else Modifier
                        MaktabaButton(onClick = onContinue, modifier = focus.testTag("tv-permissions-continue")) {
                            Text(stringResource(if (allOk) R.string.tv_perm_continue else R.string.tv_perm_not_now))
                        }
                        if (!allOk && Walkthrough.missingRequired(rows)) {
                            Text(stringResource(R.string.tv_perm_again), style = MaterialTheme.typography.bodySmall, color = MaktabaPalette.Faint, modifier = Modifier.weight(1f))
                        }
                    }
                    Text(stringResource(R.string.tv_perm_help), style = MaterialTheme.typography.bodySmall, color = MaktabaPalette.Faint)
                }
            }
        }
    }
}

private fun titleOf(p: AppPermission) = when (p.id) {
    AppPermissions.LocalNetwork.id -> R.string.tv_perm_local_network
    else -> error("the walkthrough has no words for ${p.id}")
}

private fun whyOf(p: AppPermission) = when (p.id) {
    AppPermissions.LocalNetwork.id -> R.string.tv_perm_local_network_why
    else -> error("the walkthrough has no words for ${p.id}")
}

private fun withoutOf(p: AppPermission) = when (p.id) {
    AppPermissions.LocalNetwork.id -> R.string.tv_perm_local_network_without
    else -> error("the walkthrough has no words for ${p.id}")
}

private fun stateText(state: PermissionState) = when (state) {
    PermissionState.Granted -> R.string.tv_perm_state_granted
    PermissionState.Denied -> R.string.tv_perm_state_denied
    PermissionState.DeniedPermanently -> R.string.tv_perm_state_denied_permanently
    PermissionState.NotAsked -> R.string.tv_perm_state_not_asked
    PermissionState.NotApplicable -> R.string.tv_perm_state_not_applicable
}
