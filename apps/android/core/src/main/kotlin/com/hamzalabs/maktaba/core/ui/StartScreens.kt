package com.hamzalabs.maktaba.core.ui

import android.content.Context
import android.os.Build
import android.provider.Settings
import androidx.activity.compose.BackHandler
import androidx.compose.foundation.Image
import androidx.compose.foundation.layout.width
import androidx.compose.material.icons.filled.MoreVert
import androidx.compose.material.icons.outlined.Edit
import androidx.compose.material.icons.outlined.Cloud
import androidx.compose.material.icons.outlined.QrCodeScanner
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.DropdownMenu
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.SegmentedButton
import androidx.compose.material3.SegmentedButtonDefaults
import androidx.compose.material3.SingleChoiceSegmentedButtonRow
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.ui.platform.LocalLayoutDirection
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.LiveRegionMode
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.heading
import androidx.compose.ui.semantics.liveRegion
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.unit.LayoutDirection
import androidx.compose.ui.unit.sp
import com.hamzalabs.maktaba.core.api.DeviceCode
import com.hamzalabs.maktaba.core.api.DeviceSignIn
import com.hamzalabs.maktaba.core.api.PairingCode
import com.hamzalabs.maktaba.core.session.ConnectLink
import com.hamzalabs.maktaba.core.session.ConnectProgress
import com.hamzalabs.maktaba.core.session.ConnectStage
import com.hamzalabs.maktaba.core.session.FailReason
import com.hamzalabs.maktaba.core.session.HomeProbe
import com.hamzalabs.maktaba.core.session.addressCandidates
import kotlinx.coroutines.CancellationException
import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.BoxWithConstraints
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.ColumnScope
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.imePadding
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.safeDrawingPadding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.text.KeyboardActions
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Close
import androidx.compose.material.icons.outlined.Home
import androidx.compose.material.icons.outlined.Wifi
import androidx.compose.material3.Button
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.luminance
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.LocalResources
import androidx.compose.ui.res.painterResource
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.input.ImeAction
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.text.input.PasswordVisualTransformation
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.unit.dp
import com.hamzalabs.maktaba.core.R
import com.hamzalabs.maktaba.core.api.ApiClient
import com.hamzalabs.maktaba.core.diagnostics.AppLog
import com.hamzalabs.maktaba.core.api.errorKind
import com.hamzalabs.maktaba.core.api.User
import com.hamzalabs.maktaba.core.permissions.AppPermissions
import com.hamzalabs.maktaba.core.permissions.PermissionAction
import com.hamzalabs.maktaba.core.permissions.PermissionState
import com.hamzalabs.maktaba.core.permissions.action
import com.hamzalabs.maktaba.core.permissions.ok
import com.hamzalabs.maktaba.core.permissions.rememberPermissionMemory
import com.hamzalabs.maktaba.core.permissions.rememberPermissions
import com.hamzalabs.maktaba.core.session.AppSession
import com.hamzalabs.maktaba.core.session.FoundHome
import com.hamzalabs.maktaba.core.session.Home
import com.hamzalabs.maktaba.core.session.HomeDiscovery
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch

/** Which app the start screens belong to: their name and words. */
enum class MaktabaApp(val nameRes: Int, val taglineRes: Int) {
    Player(R.string.app_player, R.string.tagline_player),
    Remote(R.string.app_remote, R.string.tagline_remote),
}

/** What every app opens with: choose a home, sign in, then the app's own content. */
@Composable
fun SessionGate(
    session: AppSession,
    app: MaktabaApp,
    content: @Composable (user: User, client: ApiClient) -> Unit,
) {
    KeepDiscovering(session)
    when (val phase = session.phase.collectAsState().value) {
        AppSession.Phase.ChoosingHome -> HomePicker(session, app)
        AppSession.Phase.SigningIn -> SignIn(session, app)
        is AppSession.Phase.SignedIn -> content(phase.user, phase.client)
    }
    // a home's link the app was opened with: over whatever shows, until confirmed or dismissed
    session.link.collectAsState().value?.let { ConnectLinkDialog(session, it) }
}

/**
 * The start screens' frame: the web's library art behind (light or dark, tall or wide), a
 * scrim, the brand (the logo, «Maktaba» and the app), and a panel for the screen's content.
 */
@Composable
internal fun StartFrame(app: MaktabaApp, content: @Composable ColumnScope.() -> Unit) {
    StartBackdrop {
        Box(
            Modifier.fillMaxSize().safeDrawingPadding().imePadding().verticalScroll(rememberScrollState()),
            contentAlignment = Alignment.TopCenter,
        ) {
            Column(
                Modifier.widthIn(max = 520.dp).fillMaxWidth().padding(horizontal = 20.dp, vertical = 28.dp),
                horizontalAlignment = Alignment.CenterHorizontally,
                verticalArrangement = Arrangement.spacedBy(20.dp),
            ) {
                StartBrand(app)
                StartPanel(content = content)
            }
        }
    }
}

/** The web's library art (light or dark, tall or wide) under the tokens' scrim, filling the screen. */
@Composable
internal fun StartBackdrop(content: @Composable () -> Unit) {
    val colors = MaterialTheme.colorScheme
    val dark = colors.background.luminance() < 0.5f
    BoxWithConstraints(Modifier.fillMaxSize().background(colors.background)) {
        val wide = maxWidth > maxHeight
        val art = when {
            dark && wide -> R.drawable.library_dark_wide
            dark -> R.drawable.library_dark_phone
            wide -> R.drawable.library_light_wide
            else -> R.drawable.library_light_phone
        }
        Image(painterResource(art), null, Modifier.fillMaxSize(), contentScale = ContentScale.Crop)
        // the tokens' scrim (`color.semantic.scrim`): the layer over the library art
        Box(Modifier.fillMaxSize().background(MaktabaTheme.brand.scrim).testTag("start-scrim"))
        content()
    }
}

/** The logo, «Maktaba» and the app's name. */
@Composable
internal fun StartBrand(app: MaktabaApp) {
    val colors = MaterialTheme.colorScheme
    Column(horizontalAlignment = Alignment.CenterHorizontally, verticalArrangement = Arrangement.spacedBy(20.dp)) {
        Image(
            painterResource(R.drawable.maktaba_logo),
            contentDescription = null,
            modifier = Modifier.size(96.dp).clip(RoundedCornerShape(22.dp)),
        )
        Column(horizontalAlignment = Alignment.CenterHorizontally) {
            Text(stringResource(R.string.brand), style = MaterialTheme.typography.displaySmall, color = colors.primary)
            Text(stringResource(app.nameRes), style = MaterialTheme.typography.titleMedium, color = colors.onSurfaceVariant)
        }
    }
}

/** The start screens' panel: a raised sheet (`color.semantic.raised`), a hairline border, rounded. */
@Composable
internal fun StartPanel(modifier: Modifier = Modifier, content: @Composable ColumnScope.() -> Unit) {
    val colors = MaterialTheme.colorScheme
    Column(
        modifier.fillMaxWidth()
            .clip(RoundedCornerShape(20.dp))
            .background(MaktabaTheme.brand.raised.copy(alpha = 0.86f))
            .border(1.dp, colors.outlineVariant, RoundedCornerShape(20.dp))
            .padding(20.dp),
        verticalArrangement = Arrangement.spacedBy(12.dp),
    ) { content() }
}

@Composable
private fun SectionTitle(text: String) {
    Text(text, style = MaterialTheme.typography.titleSmall, color = MaterialTheme.colorScheme.primary, modifier = Modifier.semantics { heading() })
}

/**
 * A home to tap: its name and address; for a home this app knows, a menu to rename or remove
 * it, and a mark when it is also on this network.
 */
@Composable
private fun HomeRow(
    name: String,
    address: String,
    onClick: () -> Unit,
    nearby: Boolean = false,
    onRename: (() -> Unit)? = null,
    onRemove: (() -> Unit)? = null,
    enabled: Boolean = true,
) {
    var menu by remember { mutableStateOf(false) }
    Row(
        Modifier.fillMaxWidth().clip(RoundedCornerShape(12.dp)).clickable(enabled = enabled, onClick = onClick)
            .padding(vertical = 8.dp, horizontal = 4.dp).testTag("home-row"),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(12.dp),
    ) {
        Box(
            Modifier.size(40.dp).clip(RoundedCornerShape(10.dp)).background(MaterialTheme.colorScheme.primaryContainer),
            contentAlignment = Alignment.Center,
        ) { Icon(Icons.Outlined.Home, null, tint = MaterialTheme.colorScheme.onPrimaryContainer) }
        Column(Modifier.weight(1f)) {
            Text(name, style = MaterialTheme.typography.titleMedium)
            // an address reads left to right in Arabic too
            CompositionLocalProvider(LocalLayoutDirection provides LayoutDirection.Ltr) {
                Text(address, style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
            }
            if (nearby) {
                Text(stringResource(R.string.home_on_network), style = MaterialTheme.typography.labelSmall, color = MaterialTheme.colorScheme.primary)
            }
        }
        if (onRename != null || onRemove != null) {
            Box {
                IconButton(onClick = { menu = true }) { Icon(Icons.Filled.MoreVert, stringResource(R.string.home_menu, name)) }
                DropdownMenu(expanded = menu, onDismissRequest = { menu = false }) {
                    onRename?.let { DropdownMenuItem(text = { Text(stringResource(R.string.home_rename)) }, onClick = { menu = false; it() }) }
                    onRemove?.let { DropdownMenuItem(text = { Text(stringResource(R.string.home_remove)) }, onClick = { menu = false; it() }) }
                }
            }
        }
    }
}

/**
 * The home picker (story 42.2), on first start and from "Switch home": the homes on this
 * network (they announce themselves over mDNS), the homes this app knows (rename, remove),
 * and adding one by its address or its QR code. A typed or scanned address is checked to be a
 * Maktaba home before the app uses it.
 */
@Composable
fun HomePicker(session: AppSession, app: MaktabaApp, probe: HomeProbe = remember { HomeProbe() }) {
    val context = LocalContext.current
    val saved by session.homes.collectAsState()
    // the walkthrough asks first (PermissionsGate); this keeps the way to allow it at hand
    val permissions = rememberPermissions(listOf(AppPermissions.LocalNetwork), rememberPermissionMemory())
    val network = permissions.rows.first()
    val granted = network.state.ok
    val found by session.nearby.collectAsState()
    var searched by remember { mutableStateOf(false) }
    var typing by rememberSaveable { mutableStateOf(false) }
    var address by rememberSaveable { mutableStateOf("") }
    var problem by remember { mutableStateOf<String?>(null) }
    val progress by session.progress.collectAsState()
    var connecting by remember { mutableStateOf(false) }
    var scanning by rememberSaveable { mutableStateOf(false) }
    var renaming by remember { mutableStateOf<Home?>(null) }
    var throughCloud by rememberSaveable { mutableStateOf(false) }
    val scope = rememberCoroutineScope()
    val invalid = stringResource(R.string.address_invalid)
    val notHome = stringResource(R.string.scan_not_home)

    LaunchedEffect(granted) {
        if (!granted) return@LaunchedEffect
        // the app's own browse (KeepDiscovering), afresh now that it may browse
        session.rediscover()
        delay(6_000)
        searched = true
    }

    fun connect(link: ConnectLink) {
        if (connecting || progress.let { it is ConnectProgress.Finding || it is ConnectProgress.Trying }) return
        connecting = true
        problem = null
        scope.launch {
            try {
                session.connect(link, probe)
            } catch (e: CancellationException) {
                throw e
            } catch (e: Exception) {
                // why, and the addresses tried, are in session.progress (Failed)
                AppLog.failure("connect", e)
            } finally {
                connecting = false
            }
        }
    }

    fun add() {
        val urls = addressCandidates(address)
        if (urls.isEmpty()) {
            problem = invalid
            return
        }
        connect(ConnectLink(null, null, urls))
    }

    if (scanning) {
        BackHandler { scanning = false }
        QrScanner(
            title = stringResource(R.string.scan_home_title),
            hint = stringResource(R.string.scan_home_hint),
            onScan = { text ->
                scanning = false
                val link = ConnectLink.parse(text)
                if (link == null) problem = notHome else connect(link)
            },
            onClose = { scanning = false },
        )
        return
    }

    if (throughCloud) {
        CloudHomes(app, session, onClose = { throughCloud = false })
        return
    }

    renaming?.let { home ->
        RenameDialog(home, onDismiss = { renaming = null }, onSave = { session.rename(home, it); renaming = null })
    }

    /** The saved home a found one is: by its exact address only (an announced id is not proof). */
    fun savedFor(f: FoundHome): Home? = saved.firstOrNull { it.url == f.url }

    StartFrame(app) {
        Text(
            stringResource(app.taglineRes),
            style = MaterialTheme.typography.bodyLarge,
            textAlign = TextAlign.Center,
            modifier = Modifier.fillMaxWidth(),
        )

        if (saved.isNotEmpty()) {
            SectionTitle(stringResource(R.string.homes_saved))
            saved.forEach { home ->
                val nearby = found.any { f -> f.url == home.url }
                HomeRow(
                    home.label,
                    home.address,
                    onClick = { session.use(home) },
                    nearby = nearby,
                    onRename = { renaming = home },
                    onRemove = { session.forget(home) },
                    enabled = !connecting,
                )
            }
            HorizontalDivider(color = MaterialTheme.colorScheme.outlineVariant)
        }

        if (!granted) {
            Row(horizontalArrangement = Arrangement.spacedBy(12.dp), verticalAlignment = Alignment.Top) {
                Icon(Icons.Outlined.Wifi, null, tint = MaterialTheme.colorScheme.primary)
                Column(Modifier.weight(1f), verticalArrangement = Arrangement.spacedBy(6.dp)) {
                    Text(stringResource(R.string.local_network_title), style = MaterialTheme.typography.titleMedium)
                    Text(stringResource(R.string.local_network_body), style = MaterialTheme.typography.bodyMedium)
                    if (network.state == PermissionState.Denied || network.state == PermissionState.DeniedPermanently) {
                        Text(stringResource(R.string.local_network_denied), style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
                    }
                    Button(onClick = { permissions.act(network.permission) }) {
                        Text(stringResource(if (network.state.action == PermissionAction.OpenSettings) R.string.permission_open_settings else R.string.local_network_allow))
                    }
                }
            }
        } else {
            SectionTitle(stringResource(R.string.homes_on_network))
            val fresh = found.filter { savedFor(it) == null }
            fresh.forEach { f ->
                HomeRow(f.name, f.url.substringAfter("://"), onClick = { session.use(Home(f.url, f.name, f.id)) }, enabled = !connecting)
            }
            if (fresh.isEmpty()) {
                StatusLine(
                    stringResource(
                        when {
                            found.isNotEmpty() -> R.string.homes_all_saved
                            searched -> R.string.homes_none
                            else -> R.string.homes_looking
                        },
                    ),
                    busy = !searched,
                )
            }
        }

        HorizontalDivider(color = MaterialTheme.colorScheme.outlineVariant)
        SectionTitle(stringResource(R.string.add_home))
        if (typing || (saved.isEmpty() && found.isEmpty() && searched)) {
            OutlinedTextField(
                value = address,
                onValueChange = { address = it; problem = null },
                label = { Text(stringResource(R.string.address_label)) },
                singleLine = true,
                enabled = !connecting,
                keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Uri, imeAction = ImeAction.Go, autoCorrectEnabled = false),
                keyboardActions = KeyboardActions(onGo = { add() }),
                modifier = Modifier.fillMaxWidth().testTag("address-field"),
            )
            Button(onClick = ::add, enabled = address.isNotBlank() && !connecting, modifier = Modifier.fillMaxWidth().testTag("address-connect")) {
                Text(stringResource(R.string.connect))
            }
        } else {
            OutlinedButton(onClick = { typing = true }, modifier = Modifier.fillMaxWidth()) {
                Icon(Icons.Outlined.Edit, null, Modifier.size(18.dp))
                Spacer(Modifier.width(8.dp))
                Text(stringResource(R.string.address_title))
            }
        }
        OutlinedButton(onClick = { problem = null; scanning = true }, enabled = !connecting, modifier = Modifier.fillMaxWidth().testTag("scan-home")) {
            Icon(Icons.Outlined.QrCodeScanner, null, Modifier.size(18.dp))
            Spacer(Modifier.width(8.dp))
            Text(stringResource(R.string.scan_qr))
        }
        // away from home: the homes linked to a Maktaba Cloud account (story 42.7)
        OutlinedButton(onClick = { problem = null; throughCloud = true }, enabled = !connecting, modifier = Modifier.fillMaxWidth().testTag("through-cloud")) {
            Icon(Icons.Outlined.Cloud, null, Modifier.size(18.dp))
            Spacer(Modifier.width(8.dp))
            Text(stringResource(R.string.cloud_title))
        }
        // adding a home: finding it, each address tried, or why none answered (Retry, attempt n)
        progress?.takeIf { it.showsOnPicker() }?.let { ConnectStatus(it, session.now, onRetry = session::retry) }
        problem?.let {
            Text(it, color = MaterialTheme.colorScheme.error, style = MaterialTheme.typography.bodyMedium, modifier = Modifier.semantics { liveRegion = LiveRegionMode.Polite })
        }
    }
}

/** A name for a home on this device; empty goes back to the home's own name. */
@Composable
private fun RenameDialog(home: Home, onDismiss: () -> Unit, onSave: (String) -> Unit) {
    var text by remember(home.url) { mutableStateOf(home.nickname ?: "") }
    AlertDialog(
        onDismissRequest = onDismiss,
        title = { Text(stringResource(R.string.rename_title, home.label)) },
        text = {
            Column(verticalArrangement = Arrangement.spacedBy(8.dp)) {
                OutlinedTextField(
                    value = text,
                    onValueChange = { text = it.take(60) },
                    label = { Text(stringResource(R.string.rename_label)) },
                    placeholder = { Text(home.name) },
                    singleLine = true,
                    keyboardOptions = KeyboardOptions(imeAction = ImeAction.Done),
                    keyboardActions = KeyboardActions(onDone = { onSave(text) }),
                    modifier = Modifier.fillMaxWidth(),
                )
                Text(stringResource(R.string.rename_hint), style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
            }
        },
        confirmButton = { TextButton(onClick = { onSave(text) }) { Text(stringResource(R.string.save)) } },
        dismissButton = { TextButton(onClick = onDismiss) { Text(stringResource(R.string.cancel)) } },
    )
}

/**
 * A `maktaba://connect` link the app was opened with: the home it names and its addresses,
 * and Connect (opening a link never switches homes by itself).
 */
@Composable
fun ConnectLinkDialog(session: AppSession, link: ConnectLink, probe: HomeProbe = remember { HomeProbe() }) {
    var busy by remember(link) { mutableStateOf(false) }
    var tried by remember(link) { mutableStateOf(false) }
    val progress by session.progress.collectAsState()
    val scope = rememberCoroutineScope()
    AlertDialog(
        onDismissRequest = { if (!busy) session.dismissLink() },
        title = { Text(stringResource(R.string.link_title, link.name ?: stringResource(R.string.link_unnamed))) },
        text = {
            Column(verticalArrangement = Arrangement.spacedBy(6.dp)) {
                Text(stringResource(R.string.link_body))
                CompositionLocalProvider(LocalLayoutDirection provides LayoutDirection.Ltr) {
                    Column { link.urls.forEach { Text(it.substringAfter("://"), style = MaterialTheme.typography.bodySmall) } }
                }
                // once Connect is pressed: each step, or why it failed and what was tried
                progress?.takeIf { tried && it.showsOnPicker() }?.let { ConnectStatus(it, session.now) }
            }
        },
        confirmButton = {
            TextButton(
                enabled = !busy,
                onClick = {
                    busy = true
                    tried = true
                    scope.launch {
                        try {
                            session.connect(link, probe)
                        } catch (e: CancellationException) {
                            throw e
                        } catch (e: Exception) {
                            // said in session.progress (Failed): the button reads Retry
                            AppLog.failure("connect link", e)
                        } finally {
                            busy = false
                        }
                    }
                },
            ) { Text(stringResource(if (progress is ConnectProgress.Failed && tried) R.string.progress_retry else R.string.connect)) }
        },
        dismissButton = { TextButton(enabled = !busy, onClick = session::dismissLink) { Text(stringResource(R.string.cancel)) } },
    )
}

/**
 * Sign in to the chosen home, by its name (the home's own once it has said it): with a
 * username and password, or with a code a person signed in elsewhere approves (story 42.6).
 */
@Composable
fun SignIn(session: AppSession, app: MaktabaApp) {
    val home by session.current.collectAsState()
    var byCode by rememberSaveable { mutableStateOf(false) }
    val name = home?.label ?: ""
    val progress by session.progress.collectAsState()
    StartFrame(app) {
        // on launch: reconnecting, the home busy (its countdown), the home just found, or why
        // the kept sign-in could not be checked (Retry, attempt n)
        progress?.takeIf { it.showsOverSignIn() }?.let { p ->
            ConnectStatus(
                p,
                session.now,
                onRetry = if ((p as? ConnectProgress.Failed)?.stage == ConnectStage.Restore) session::retryRestore else session::retry,
            )
        }
        Column(Modifier.fillMaxWidth(), horizontalAlignment = Alignment.CenterHorizontally) {
            Text(stringResource(R.string.signin_to, name), style = MaterialTheme.typography.headlineSmall, textAlign = TextAlign.Center)
            CompositionLocalProvider(LocalLayoutDirection provides LayoutDirection.Ltr) {
                Text(home?.address ?: "", style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
            }
        }
        SingleChoiceSegmentedButtonRow(Modifier.fillMaxWidth()) {
            SegmentedButton(
                selected = !byCode,
                onClick = { byCode = false },
                shape = SegmentedButtonDefaults.itemShape(0, 2),
                modifier = Modifier.testTag("signin-password-tab"),
            ) { Text(stringResource(R.string.signin_password_tab)) }
            SegmentedButton(
                selected = byCode,
                onClick = { byCode = true },
                shape = SegmentedButtonDefaults.itemShape(1, 2),
                modifier = Modifier.testTag("signin-code-tab"),
            ) { Text(stringResource(R.string.signin_code_tab)) }
        }
        if (byCode) {
            CodeSignIn(session, name, onUsePassword = { byCode = false })
        } else {
            PasswordSignIn(session)
        }
        // a home chosen in Maktaba Cloud, at an address the cloud gave: no password needed
        val h = home
        if (h != null && session.cloud?.signedIn == true && h.url in h.cloudAddresses) CloudSignInButton(session)
        Spacer(Modifier.height(4.dp))
        TextButton(onClick = session::switchHome, modifier = Modifier.align(Alignment.CenterHorizontally)) {
            Text(stringResource(R.string.other_home))
        }
    }
}

@Composable
private fun PasswordSignIn(session: AppSession) {
    var username by rememberSaveable { mutableStateOf("") }
    var password by remember { mutableStateOf("") }
    var busy by remember { mutableStateOf(false) }
    val scope = rememberCoroutineScope()
    val progress by session.progress.collectAsState()
    // this form's own step: signing in, or why it failed (a wrong password, no answer, ...)
    val mine = progress?.takeIf {
        (it is ConnectProgress.SigningIn && it.stage == ConnectStage.Password) ||
            (it is ConnectProgress.Failed && it.stage == ConnectStage.Password)
    }
    val wrong = (mine as? ConnectProgress.Failed)?.reason == FailReason.WrongPassword
    fun signIn() {
        if (username.isEmpty() || password.isEmpty() || busy) return
        busy = true
        scope.launch {
            try {
                session.signIn(username, password)
            } catch (e: CancellationException) {
                throw e
            } catch (e: Exception) {
                // said in session.progress (Failed)
                AppLog.failure("sign in", e)
            } finally {
                busy = false
            }
        }
    }
    OutlinedTextField(
        value = username,
        onValueChange = { username = it },
        label = { Text(stringResource(R.string.username)) },
        singleLine = true,
        keyboardOptions = KeyboardOptions(imeAction = ImeAction.Next, autoCorrectEnabled = false),
        modifier = Modifier.fillMaxWidth(),
    )
    OutlinedTextField(
        value = password,
        onValueChange = { password = it },
        label = { Text(stringResource(R.string.password)) },
        singleLine = true,
        visualTransformation = PasswordVisualTransformation(),
        keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Password, imeAction = ImeAction.Done),
        keyboardActions = KeyboardActions(onDone = { signIn() }),
        isError = wrong,
        modifier = Modifier.fillMaxWidth(),
    )
    Button(
        onClick = ::signIn,
        enabled = username.isNotEmpty() && password.isNotEmpty() && !busy,
        modifier = Modifier.fillMaxWidth().height(48.dp),
    ) {
        Text(stringResource(if (busy) R.string.progress_signing_in_button else R.string.signin_submit))
    }
    mine?.let { ConnectStatus(it, session.now, onRetry = if (wrong) null else ::signIn) }
}

/**
 * Sign-in by code: the home's code, large, and a QR of the page that approves it; this device
 * waits (polling) until a person signed in elsewhere approves, then it is signed in as them.
 */
@Composable
private fun ColumnScope.CodeSignIn(session: AppSession, name: String, onUsePassword: () -> Unit) {
    val context = LocalContext.current
    val resources = LocalResources.current
    var code by remember { mutableStateOf<DeviceCode?>(null) }
    var unsupported by remember { mutableStateOf(false) }
    var problem by remember { mutableStateOf<String?>(null) }
    var attempt by remember { mutableStateOf(0) }
    val progress by session.progress.collectAsState()
    // this tab's own step: signing in, the code waiting for approval, or why it ended
    val mine = progress?.takeIf {
        (it is ConnectProgress.SigningIn && it.stage == ConnectStage.Code) || it is ConnectProgress.AwaitingApproval ||
            (it is ConnectProgress.Failed && it.stage == ConnectStage.Code)
    }
    LaunchedEffect(attempt) {
        code = null
        problem = null
        try {
            // a code that runs out ends the wait: "expired", and Retry asks for a new one
            session.signInWithCode(deviceLabel(context), onCode = { code = it }, renew = false)
        } catch (e: CancellationException) {
            throw e
        } catch (e: DeviceSignIn.Unsupported) {
            unsupported = true
        } catch (e: Exception) {
            // said in session.progress (Failed); the sentence is kept for a failure it did not say
            AppLog.failure("sign in by code", e)
            problem = resources.getString(e.errorKind().sentence())
        }
    }
    val current = code
    when {
        unsupported -> {
            Text(stringResource(R.string.code_unsupported, name), style = MaterialTheme.typography.bodyMedium)
            Button(onClick = onUsePassword, modifier = Modifier.fillMaxWidth()) { Text(stringResource(R.string.signin_password_tab)) }
        }
        mine is ConnectProgress.Failed -> ConnectStatus(mine, session.now, onRetry = { attempt++ })
        problem != null -> {
            Text(problem ?: "", color = MaterialTheme.colorScheme.error, style = MaterialTheme.typography.bodyMedium)
            Button(onClick = { attempt++ }, modifier = Modifier.fillMaxWidth()) { Text(stringResource(R.string.progress_retry)) }
        }
        current == null -> mine?.let { ConnectStatus(it, session.now) }
            ?: StatusLine(stringResource(R.string.code_starting, name))
        else -> {
            val shown = PairingCode.shown(current.code)
            Text(stringResource(R.string.code_intro, name), style = MaterialTheme.typography.bodyMedium)
            // the code reads left to right in Arabic too, one character at a time for TalkBack
            CompositionLocalProvider(LocalLayoutDirection provides LayoutDirection.Ltr) {
                Text(
                    shown,
                    style = MaterialTheme.typography.displayMedium.copy(fontFamily = FontFamily.Monospace, letterSpacing = 4.sp),
                    color = MaterialTheme.colorScheme.primary,
                    textAlign = TextAlign.Center,
                    modifier = Modifier.fillMaxWidth().testTag("signin-code")
                        .semantics { contentDescription = shown.toCharArray().joinToString(" ") },
                )
            }
            val target = session.client?.let { current.approveUrl(it.home) }
            if (target != null) {
                Text(stringResource(R.string.code_scan), style = MaterialTheme.typography.bodyMedium)
                QrCode(
                    target,
                    stringResource(R.string.code_qr_description, shown),
                    Modifier.align(Alignment.CenterHorizontally).widthIn(max = 220.dp).fillMaxWidth(0.6f),
                )
            }
            // waiting for approval, the code and its live countdown
            mine?.let { ConnectStatus(it, session.now) } ?: StatusLine(stringResource(R.string.code_waiting))
        }
    }
}

/** A step the home picker shows: adding a home (finding, trying, why it failed). */
internal fun ConnectProgress.showsOnPicker(): Boolean = when (this) {
    is ConnectProgress.Finding, is ConnectProgress.Trying -> true
    is ConnectProgress.Failed -> stage == ConnectStage.Connect || stage == ConnectStage.Cloud
    else -> false
}

/** A step shown over the sign-in form: on launch (reconnecting, busy, other addresses), found, or why. */
internal fun ConnectProgress.showsOverSignIn(): Boolean = when (this) {
    is ConnectProgress.Reconnecting, is ConnectProgress.HomeBusy, is ConnectProgress.Found,
    is ConnectProgress.Finding, is ConnectProgress.Trying, is ConnectProgress.LookingNearby,
    -> true
    is ConnectProgress.Failed -> stage == ConnectStage.Restore || stage == ConnectStage.Cloud
    else -> false
}

/** This device's name for the home's list of signed-in devices: the one its owner gave it, else its model. */
fun deviceLabel(context: Context): String {
    val named = runCatching { Settings.Global.getString(context.contentResolver, Settings.Global.DEVICE_NAME) }.getOrNull()
    return named?.trim()?.takeIf { it.isNotEmpty() }
        ?: listOf(Build.MANUFACTURER, Build.MODEL).filterNot { it.isNullOrBlank() }.joinToString(" ").ifBlank { "Android" }
}
