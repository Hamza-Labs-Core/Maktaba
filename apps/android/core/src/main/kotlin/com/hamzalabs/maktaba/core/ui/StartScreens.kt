package com.hamzalabs.maktaba.core.ui

import android.content.pm.PackageManager
import android.os.Build
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.Image
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
import androidx.compose.material3.CircularProgressIndicator
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
import androidx.compose.ui.graphics.Brush
import androidx.compose.ui.graphics.luminance
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.res.painterResource
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.input.ImeAction
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.text.input.PasswordVisualTransformation
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.unit.dp
import androidx.core.content.ContextCompat
import com.hamzalabs.maktaba.core.R
import com.hamzalabs.maktaba.core.api.ApiClient
import com.hamzalabs.maktaba.core.api.ApiException
import com.hamzalabs.maktaba.core.api.User
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
    when (val phase = session.phase.collectAsState().value) {
        AppSession.Phase.ChoosingHome -> HomePicker(session, app)
        AppSession.Phase.SigningIn -> SignIn(session, app)
        is AppSession.Phase.SignedIn -> content(phase.user, phase.client)
    }
}

/**
 * The start screens' frame: the web's library art behind (light or dark, tall or wide), a
 * scrim, the brand (the logo, «Maktaba» and the app), and a panel for the screen's content.
 */
@Composable
private fun StartFrame(app: MaktabaApp, content: @Composable ColumnScope.() -> Unit) {
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
        Box(
            Modifier.fillMaxSize().background(
                Brush.verticalGradient(listOf(colors.background.copy(alpha = 0.45f), colors.background.copy(alpha = 0.85f))),
            ),
        )
        Box(
            Modifier.fillMaxSize().safeDrawingPadding().imePadding().verticalScroll(rememberScrollState()),
            contentAlignment = Alignment.TopCenter,
        ) {
            Column(
                Modifier.widthIn(max = 520.dp).fillMaxWidth().padding(horizontal = 20.dp, vertical = 28.dp),
                horizontalAlignment = Alignment.CenterHorizontally,
                verticalArrangement = Arrangement.spacedBy(20.dp),
            ) {
                Image(
                    painterResource(R.drawable.maktaba_logo),
                    contentDescription = null,
                    modifier = Modifier.size(96.dp).clip(RoundedCornerShape(22.dp)),
                )
                Column(horizontalAlignment = Alignment.CenterHorizontally) {
                    Text(stringResource(R.string.brand), style = MaterialTheme.typography.displaySmall, color = colors.primary)
                    Text(stringResource(app.nameRes), style = MaterialTheme.typography.titleMedium, color = colors.onSurfaceVariant)
                }
                Column(
                    Modifier.fillMaxWidth()
                        .clip(RoundedCornerShape(20.dp))
                        .background(colors.surface.copy(alpha = 0.86f))
                        .border(1.dp, colors.outlineVariant, RoundedCornerShape(20.dp))
                        .padding(20.dp),
                    verticalArrangement = Arrangement.spacedBy(12.dp),
                ) { content() }
            }
        }
    }
}

@Composable
private fun SectionTitle(text: String) {
    Text(text, style = MaterialTheme.typography.titleSmall, color = MaterialTheme.colorScheme.primary)
}

/** A home to tap: its name, its address, and a mark when it is saved; a forget button for a saved one. */
@Composable
private fun HomeRow(name: String, address: String, saved: Boolean, onClick: () -> Unit, onForget: (() -> Unit)? = null) {
    Row(
        Modifier.fillMaxWidth().clip(RoundedCornerShape(12.dp)).clickable(onClick = onClick).padding(vertical = 8.dp, horizontal = 4.dp),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(12.dp),
    ) {
        Box(
            Modifier.size(40.dp).clip(RoundedCornerShape(10.dp)).background(MaterialTheme.colorScheme.primaryContainer),
            contentAlignment = Alignment.Center,
        ) { Icon(Icons.Outlined.Home, null, tint = MaterialTheme.colorScheme.onPrimaryContainer) }
        Column(Modifier.weight(1f)) {
            Text(name, style = MaterialTheme.typography.titleMedium)
            Text(address, style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
        }
        if (saved && onForget == null) {
            Text(stringResource(R.string.home_saved), style = MaterialTheme.typography.labelMedium, color = MaterialTheme.colorScheme.primary)
        }
        if (onForget != null) {
            IconButton(onClick = onForget) { Icon(Icons.Filled.Close, stringResource(R.string.home_forget, name)) }
        }
    }
}

/** Android 17 and later ask before an app reaches the local network, where a home is. */
private const val LOCAL_NETWORK_SDK = 37
private const val ACCESS_LOCAL_NETWORK = "android.permission.ACCESS_LOCAL_NETWORK"

/** Whether this app may reach the local network, and a way to ask. */
private class LocalNetwork(val granted: Boolean, val denied: Boolean, val ask: () -> Unit)

@Composable
private fun rememberLocalNetwork(): LocalNetwork {
    val context = LocalContext.current
    fun has() = Build.VERSION.SDK_INT < LOCAL_NETWORK_SDK ||
        ContextCompat.checkSelfPermission(context, ACCESS_LOCAL_NETWORK) == PackageManager.PERMISSION_GRANTED
    var granted by remember { mutableStateOf(has()) }
    var denied by remember { mutableStateOf(false) }
    val launcher = rememberLauncherForActivityResult(ActivityResultContracts.RequestPermission()) {
        granted = it
        denied = !it
    }
    return LocalNetwork(granted, denied) { launcher.launch(ACCESS_LOCAL_NETWORK) }
}

/**
 * The homes on this network (they announce themselves over mDNS), the homes this app knows,
 * and an address to type when a home is elsewhere or does not answer.
 */
@Composable
fun HomePicker(session: AppSession, app: MaktabaApp) {
    val context = LocalContext.current
    val saved by session.homes.collectAsState()
    val network = rememberLocalNetwork()
    var found by remember { mutableStateOf<List<FoundHome>>(emptyList()) }
    var searched by remember { mutableStateOf(false) }
    var typing by remember { mutableStateOf(false) }
    var address by remember { mutableStateOf("") }
    var problem by remember { mutableStateOf<String?>(null) }
    val invalid = stringResource(R.string.address_invalid)

    LaunchedEffect(Unit) { if (!network.granted) network.ask() }
    LaunchedEffect(network.granted) {
        if (!network.granted) return@LaunchedEffect
        launch { delay(6_000); searched = true }
        runCatching { HomeDiscovery(context.applicationContext).homes().collect { found = it } }
        searched = true
    }

    fun add() {
        val url = Home.normalize(address)
        if (url == null) {
            problem = invalid
            return
        }
        problem = null
        session.use(saved.firstOrNull { it.url == url } ?: Home(url, url.substringAfter("://")))
    }

    StartFrame(app) {
        Text(
            stringResource(app.taglineRes),
            style = MaterialTheme.typography.bodyLarge,
            textAlign = TextAlign.Center,
            modifier = Modifier.fillMaxWidth(),
        )

        if (!network.granted) {
            Row(horizontalArrangement = Arrangement.spacedBy(12.dp), verticalAlignment = Alignment.Top) {
                Icon(Icons.Outlined.Wifi, null, tint = MaterialTheme.colorScheme.primary)
                Column(Modifier.weight(1f), verticalArrangement = Arrangement.spacedBy(6.dp)) {
                    Text(stringResource(R.string.local_network_title), style = MaterialTheme.typography.titleMedium)
                    Text(stringResource(R.string.local_network_body), style = MaterialTheme.typography.bodyMedium)
                    if (network.denied) {
                        Text(stringResource(R.string.local_network_denied), style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
                    }
                    Button(onClick = network.ask) { Text(stringResource(R.string.local_network_allow)) }
                }
            }
        } else {
            SectionTitle(stringResource(R.string.homes_on_network))
            val fresh = found.filter { f -> saved.none { it.url == f.url } }
            found.filter { f -> saved.any { it.url == f.url } }.forEach { f ->
                val home = saved.first { it.url == f.url }
                HomeRow(home.name, f.url.substringAfter("://"), saved = true, onClick = { session.use(home) })
            }
            fresh.forEach { f -> HomeRow(f.name, f.url.substringAfter("://"), saved = false, onClick = { session.use(Home(f.url, f.name)) }) }
            if (found.isEmpty()) {
                Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(12.dp)) {
                    if (!searched) CircularProgressIndicator(Modifier.size(18.dp), strokeWidth = 2.dp)
                    Text(
                        stringResource(if (searched) R.string.homes_none else R.string.homes_looking),
                        style = MaterialTheme.typography.bodyMedium,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                    )
                }
            }
        }

        val elsewhere = saved.filter { s -> found.none { it.url == s.url } }
        if (elsewhere.isNotEmpty()) {
            HorizontalDivider(color = MaterialTheme.colorScheme.outlineVariant)
            SectionTitle(stringResource(R.string.homes_saved))
            elsewhere.forEach { home ->
                HomeRow(home.name, home.url.substringAfter("://"), saved = true, onClick = { session.use(home) }, onForget = { session.forget(home) })
            }
        }

        HorizontalDivider(color = MaterialTheme.colorScheme.outlineVariant)
        if (!typing && (found.isNotEmpty() || saved.isNotEmpty() || !searched)) {
            TextButton(onClick = { typing = true }) { Text(stringResource(R.string.address_title)) }
        } else {
            SectionTitle(stringResource(R.string.address_title))
            OutlinedTextField(
                value = address,
                onValueChange = { address = it },
                label = { Text(stringResource(R.string.address_label)) },
                singleLine = true,
                keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Uri, imeAction = ImeAction.Go, autoCorrectEnabled = false),
                keyboardActions = KeyboardActions(onGo = { add() }),
                isError = problem != null,
                supportingText = problem?.let { { Text(it) } },
                modifier = Modifier.fillMaxWidth(),
            )
            Button(onClick = ::add, enabled = address.isNotBlank(), modifier = Modifier.fillMaxWidth()) { Text(stringResource(R.string.connect)) }
        }
    }
}

/** Sign in to the chosen home, by its name (the home's own once it has said it). */
@Composable
fun SignIn(session: AppSession, app: MaktabaApp) {
    val home by session.current.collectAsState()
    var username by remember { mutableStateOf("") }
    var password by remember { mutableStateOf("") }
    var busy by remember { mutableStateOf(false) }
    var problem by remember { mutableStateOf<String?>(null) }
    val scope = rememberCoroutineScope()
    val name = home?.name ?: ""
    val wrong = stringResource(R.string.signin_wrong)
    val unreachable = stringResource(R.string.signin_unreachable, name)
    fun signIn() {
        if (username.isEmpty() || password.isEmpty() || busy) return
        busy = true
        problem = null
        scope.launch {
            try {
                session.signIn(username, password)
            } catch (e: ApiException.Status) {
                problem = if (e.code in 400..403) wrong else e.message
            } catch (e: ApiException.Transport) {
                problem = unreachable
            } catch (e: Exception) {
                problem = e.message
            } finally {
                busy = false
            }
        }
    }
    StartFrame(app) {
        Column(Modifier.fillMaxWidth(), horizontalAlignment = Alignment.CenterHorizontally) {
            Text(stringResource(R.string.signin_to, name), style = MaterialTheme.typography.headlineSmall, textAlign = TextAlign.Center)
            Text(home?.url?.substringAfter("://") ?: "", style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
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
            isError = problem != null,
            supportingText = problem?.let { { Text(it) } },
            modifier = Modifier.fillMaxWidth(),
        )
        Button(
            onClick = ::signIn,
            enabled = username.isNotEmpty() && password.isNotEmpty() && !busy,
            modifier = Modifier.fillMaxWidth().height(48.dp),
        ) {
            if (busy) CircularProgressIndicator(Modifier.size(18.dp), strokeWidth = 2.dp) else Text(stringResource(R.string.signin_submit))
        }
        Spacer(Modifier.height(4.dp))
        TextButton(onClick = session::switchHome, modifier = Modifier.align(Alignment.CenterHorizontally)) {
            Text(stringResource(R.string.other_home))
        }
    }
}
