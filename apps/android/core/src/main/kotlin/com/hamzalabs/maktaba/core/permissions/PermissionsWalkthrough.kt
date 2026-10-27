package com.hamzalabs.maktaba.core.permissions

import android.app.Activity
import android.content.Context
import android.content.Intent
import android.content.pm.PackageManager
import android.net.Uri
import android.os.Build
import android.provider.Settings
import androidx.activity.compose.BackHandler
import androidx.activity.compose.LocalActivity
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.ColumnScope
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.safeDrawingPadding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.CheckCircle
import androidx.compose.material.icons.outlined.ErrorOutline
import androidx.compose.material.icons.outlined.QrCodeScanner
import androidx.compose.material.icons.outlined.Wifi
import androidx.compose.material3.Button
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
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
import androidx.compose.ui.input.pointer.pointerInput
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.semantics.clearAndSetSemantics
import androidx.compose.ui.semantics.heading
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.unit.dp
import androidx.core.app.ActivityCompat
import androidx.core.content.ContextCompat
import androidx.core.net.toUri
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.compose.LifecycleEventEffect
import com.hamzalabs.maktaba.core.R
import com.hamzalabs.maktaba.core.layout.HingeSplit
import com.hamzalabs.maktaba.core.layout.currentPanes
import com.hamzalabs.maktaba.core.session.PreferencesStore
import com.hamzalabs.maktaba.core.ui.MaktabaApp
import com.hamzalabs.maktaba.core.ui.StartBackdrop
import com.hamzalabs.maktaba.core.ui.StartBrand
import com.hamzalabs.maktaba.core.ui.StartPanel

/** The app's walkthrough memory (its own preferences file). */
@Composable
fun rememberPermissionMemory(): PermissionMemory {
    val context = LocalContext.current.applicationContext
    return remember(context) { PermissionMemory(PreferencesStore(context, "permissions")) }
}

/** Permissions as they stand now, and [act]: ask, or open the settings page that turns one on. */
class Permissions internal constructor(val rows: List<PermissionRow>, val act: (AppPermission) -> Unit)

/** Where [permission] stands on this device, read now. */
fun readPermission(context: Context, activity: Activity?, memory: PermissionMemory, permission: AppPermission): PermissionRow {
    val granted = Build.VERSION.SDK_INT < permission.sinceSdk ||
        ContextCompat.checkSelfPermission(context, permission.permission) == PackageManager.PERMISSION_GRANTED
    val rationale = activity != null && Build.VERSION.SDK_INT >= permission.sinceSdk &&
        ActivityCompat.shouldShowRequestPermissionRationale(activity, permission.permission)
    return PermissionRow(permission, permissionState(Build.VERSION.SDK_INT, permission, granted, memory.asked(permission), rationale))
}

/**
 * [permissions] as they stand, read again every time the app resumes (back from Android's
 * dialog, from Settings, or to the foreground) and after each answer.
 */
@Composable
fun rememberPermissions(permissions: List<AppPermission>, memory: PermissionMemory): Permissions {
    val context = LocalContext.current
    val activity = LocalActivity.current
    fun read() = permissions.map { readPermission(context, activity, memory, it) }
    var rows by remember(permissions) { mutableStateOf(read()) }
    LifecycleEventEffect(Lifecycle.Event.ON_RESUME) { rows = read() }
    val launcher = rememberLauncherForActivityResult(ActivityResultContracts.RequestPermission()) { rows = read() }
    return Permissions(rows) { p ->
        when (rows.firstOrNull { it.permission == p }?.state?.action) {
            PermissionAction.Ask -> {
                memory.markAsked(p)
                launcher.launch(p.permission)
            }
            PermissionAction.OpenSettings -> openSettings(context, p.settings)
            PermissionAction.None, null -> {}
        }
    }
}

/** The settings page for [pane]; the app's own page when a device has no such page. */
fun settingsIntent(context: Context, pane: SettingsPane): Intent = when (pane) {
    SettingsPane.AppNotifications -> Intent(Settings.ACTION_APP_NOTIFICATION_SETTINGS)
        .putExtra(Settings.EXTRA_APP_PACKAGE, context.packageName)
    SettingsPane.AppDetails -> appDetailsIntent(context)
}

private fun appDetailsIntent(context: Context) =
    Intent(Settings.ACTION_APPLICATION_DETAILS_SETTINGS, Uri.fromParts("package", context.packageName, null))

private fun openSettings(context: Context, pane: SettingsPane) {
    runCatching { context.startActivity(settingsIntent(context, pane)) }
        .onFailure { runCatching { context.startActivity(appDetailsIntent(context)) } }
}

/**
 * The app behind the permissions walkthrough. The walkthrough shows on the first run, and
 * again whenever a required permission is missing: checked each time the app resumes.
 * «Not now» puts it away until the app next comes back to the foreground. The app stays
 * underneath (a playing video is not lost); [helpUrl] is the help page's address when a home
 * is known.
 */
@Composable
fun PermissionsGate(
    app: MaktabaApp,
    permissions: List<AppPermission> = AppPermissions.phoneApps,
    helpUrl: () -> String? = { null },
    content: @Composable () -> Unit,
) {
    val memory = rememberPermissionMemory()
    val state = rememberPermissions(permissions, memory)
    var seen by remember { mutableStateOf(memory.seen) }
    var dismissed by rememberSaveable { mutableStateOf(false) }
    // back in the foreground: a required permission still missing shows the walkthrough again
    LifecycleEventEffect(Lifecycle.Event.ON_START) { dismissed = false }
    val showing = Walkthrough.shouldShow(seen, state.rows, dismissed)
    val context = LocalContext.current
    Box(Modifier.fillMaxSize()) {
        Box(if (showing) Modifier.clearAndSetSemantics {} else Modifier) { content() }
        if (showing) {
            val done = {
                memory.seen = true
                seen = true
                dismissed = true
            }
            BackHandler(onBack = done)
            PermissionsWalkthrough(
                app = app,
                rows = Walkthrough.shown(state.rows),
                onAction = state.act,
                onContinue = done,
                onLearnMore = helpUrl()?.let { url -> { runCatching { context.startActivity(Intent(Intent.ACTION_VIEW, url.toUri())) } } },
            )
        }
    }
}

/** The help page's permissions section on [home] (a home's web address). */
fun permissionsHelpUrl(home: String, topic: String = "android-apps"): String = "${home.trimEnd('/')}/help/$topic#permissions"

private class PermissionText(val title: Int, val why: Int, val without: Int, val icon: ImageVector)

private fun textFor(p: AppPermission) = when (p.id) {
    AppPermissions.LocalNetwork.id -> PermissionText(R.string.permission_local_network, R.string.local_network_body, R.string.local_network_denied, Icons.Outlined.Wifi)
    AppPermissions.Camera.id -> PermissionText(R.string.permission_camera, R.string.camera_body, R.string.camera_denied, Icons.Outlined.QrCodeScanner)
    else -> error("the walkthrough has no words for ${p.id}")
}

/**
 * The walkthrough: what each permission is for, where it stands and a button that asks (or
 * opens Settings when Android will not ask again), then Continue. Half folded, the brand and
 * the words go on the first half and the permissions on the second.
 */
@Composable
fun PermissionsWalkthrough(
    app: MaktabaApp,
    rows: List<PermissionRow>,
    onAction: (AppPermission) -> Unit,
    onContinue: () -> Unit,
    onLearnMore: (() -> Unit)?,
) {
    val panes = currentPanes()
    StartBackdrop {
        Box(
            Modifier.fillMaxSize()
                // takes the touches (the screen under it gets none) without merging what it holds
                // into one node for TalkBack, as a clickable would
                .pointerInput(Unit) {}
                .testTag("permissions-walkthrough"),
        ) {
            if (panes.split) {
                HingeSplit(
                    panes,
                    Modifier.safeDrawingPadding(),
                    first = { Scrolling { StartBrand(app); Intro() } },
                    second = { Scrolling { StartPanel { PermissionList(rows, onAction, onContinue, onLearnMore) } } },
                )
            } else {
                Box(Modifier.fillMaxSize().safeDrawingPadding()) {
                    Scrolling {
                        StartBrand(app)
                        StartPanel {
                            Intro()
                            PermissionList(rows, onAction, onContinue, onLearnMore)
                        }
                    }
                }
            }
        }
    }
}

@Composable
private fun Scrolling(content: @Composable ColumnScope.() -> Unit) {
    Box(Modifier.fillMaxSize().verticalScroll(rememberScrollState()), contentAlignment = Alignment.TopCenter) {
        Column(
            Modifier.widthIn(max = 520.dp).fillMaxWidth().padding(horizontal = 20.dp, vertical = 28.dp),
            horizontalAlignment = Alignment.CenterHorizontally,
            verticalArrangement = Arrangement.spacedBy(20.dp),
            content = content,
        )
    }
}

@Composable
private fun Intro() {
    Column(Modifier.fillMaxWidth(), verticalArrangement = Arrangement.spacedBy(6.dp)) {
        Text(
            stringResource(R.string.permissions_title),
            style = MaterialTheme.typography.headlineSmall,
            modifier = Modifier.semantics { heading() },
        )
        Text(
            stringResource(R.string.permissions_intro),
            style = MaterialTheme.typography.bodyMedium,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
        )
    }
}

@Composable
private fun PermissionList(
    rows: List<PermissionRow>,
    onAction: (AppPermission) -> Unit,
    onContinue: () -> Unit,
    onLearnMore: (() -> Unit)?,
) {
    rows.forEach { PermissionItem(it, onAction) }
    val allOk = rows.all { it.state.ok }
    // the permission's own button leads while one is missing
    if (allOk) {
        Button(onClick = onContinue, modifier = Modifier.fillMaxWidth().testTag("permissions-continue")) {
            Text(stringResource(R.string.permissions_continue))
        }
    } else {
        OutlinedButton(onClick = onContinue, modifier = Modifier.fillMaxWidth().testTag("permissions-continue")) {
            Text(stringResource(R.string.permissions_not_now))
        }
    }
    if (!allOk && Walkthrough.missingRequired(rows)) {
        Text(
            stringResource(R.string.permissions_again),
            style = MaterialTheme.typography.bodySmall,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
            textAlign = TextAlign.Center,
            modifier = Modifier.fillMaxWidth(),
        )
    }
    if (onLearnMore != null) {
        TextButton(onClick = onLearnMore, modifier = Modifier.fillMaxWidth()) { Text(stringResource(R.string.permissions_learn_more)) }
    }
}

@Composable
private fun PermissionItem(row: PermissionRow, onAction: (AppPermission) -> Unit) {
    val colors = MaterialTheme.colorScheme
    val text = textFor(row.permission)
    val refused = row.state == PermissionState.Denied || row.state == PermissionState.DeniedPermanently
    Row(
        Modifier.fillMaxWidth()
            .border(1.dp, colors.outlineVariant, RoundedCornerShape(14.dp))
            .padding(14.dp)
            .testTag("permission-${row.permission.id}"),
        horizontalArrangement = Arrangement.spacedBy(12.dp),
        verticalAlignment = Alignment.Top,
    ) {
        Box(
            Modifier.size(40.dp).background(colors.primaryContainer, RoundedCornerShape(10.dp)),
            contentAlignment = Alignment.Center,
        ) { Icon(text.icon, null, tint = colors.onPrimaryContainer) }
        Column(Modifier.weight(1f), verticalArrangement = Arrangement.spacedBy(6.dp)) {
            Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                Text(stringResource(text.title), style = MaterialTheme.typography.titleMedium, modifier = Modifier.weight(1f, fill = false))
                Text(
                    stringResource(if (row.permission.need == Need.Required) R.string.permission_required else R.string.permission_optional),
                    style = MaterialTheme.typography.labelSmall,
                    color = colors.primary,
                    modifier = Modifier.border(1.dp, colors.primary, RoundedCornerShape(50)).padding(horizontal = 8.dp, vertical = 2.dp),
                )
            }
            Text(stringResource(text.why), style = MaterialTheme.typography.bodyMedium)
            Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(6.dp)) {
                val good = row.state.ok
                Icon(
                    if (good) Icons.Filled.CheckCircle else Icons.Outlined.ErrorOutline,
                    null,
                    tint = if (good) colors.primary else if (refused) colors.error else colors.onSurfaceVariant,
                    modifier = Modifier.size(18.dp),
                )
                Text(
                    stringResource(stateText(row.state)),
                    style = MaterialTheme.typography.labelLarge,
                    color = if (refused) colors.error else colors.onSurfaceVariant,
                    modifier = Modifier.testTag("permission-${row.permission.id}-state"),
                )
            }
            if (refused) Text(stringResource(text.without), style = MaterialTheme.typography.bodySmall, color = colors.onSurfaceVariant)
            if (row.state == PermissionState.DeniedPermanently) {
                Text(stringResource(R.string.permission_settings_hint), style = MaterialTheme.typography.bodySmall, color = colors.onSurfaceVariant)
            }
            when (row.state.action) {
                PermissionAction.Ask -> Button(onClick = { onAction(row.permission) }, Modifier.testTag("permission-${row.permission.id}-ask")) {
                    Text(stringResource(R.string.local_network_allow))
                }
                PermissionAction.OpenSettings -> Button(onClick = { onAction(row.permission) }, Modifier.testTag("permission-${row.permission.id}-settings")) {
                    Text(stringResource(R.string.permission_open_settings))
                }
                PermissionAction.None -> {}
            }
        }
    }
}

private fun stateText(state: PermissionState) = when (state) {
    PermissionState.Granted -> R.string.permission_state_granted
    PermissionState.Denied -> R.string.permission_state_denied
    PermissionState.DeniedPermanently -> R.string.permission_state_denied_permanently
    PermissionState.NotAsked -> R.string.permission_state_not_asked
    PermissionState.NotApplicable -> R.string.permission_state_not_applicable
}
