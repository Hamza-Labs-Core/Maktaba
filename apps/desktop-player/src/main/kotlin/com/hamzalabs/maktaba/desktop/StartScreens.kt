package com.hamzalabs.maktaba.desktop

import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.text.KeyboardActions
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Button
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.PrimaryTabRow
import androidx.compose.material3.Tab
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
import androidx.compose.ui.semantics.LiveRegionMode
import androidx.compose.ui.semantics.heading
import androidx.compose.ui.semantics.liveRegion
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.input.ImeAction
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.text.input.PasswordVisualTransformation
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import com.hamzalabs.maktaba.core.api.ApiException
import com.hamzalabs.maktaba.core.api.DeviceCode
import com.hamzalabs.maktaba.core.api.DeviceSignIn
import com.hamzalabs.maktaba.core.api.PairingCode
import com.hamzalabs.maktaba.core.session.AppSession
import com.hamzalabs.maktaba.core.session.ConnectLink
import com.hamzalabs.maktaba.core.session.FoundHome
import com.hamzalabs.maktaba.core.session.Home
import com.hamzalabs.maktaba.core.session.HomeProbe
import com.hamzalabs.maktaba.core.session.NotAHome
import com.hamzalabs.maktaba.core.session.addressCandidates
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.launch
import java.net.InetAddress

/** What the person typed: a `maktaba://connect` link, else an address (with its path, then its root). */
fun linkFor(text: String): ConnectLink? =
    ConnectLink.parse(text) ?: addressCandidates(text).takeIf { it.isNotEmpty() }?.let { ConnectLink(null, null, it) }

@Composable
fun Heading(text: String) {
    Text(text, style = MaterialTheme.typography.headlineSmall, modifier = Modifier.semantics { heading() })
}

@Composable
fun Problem(text: String) {
    Text(
        text,
        color = MaterialTheme.colorScheme.error,
        modifier = Modifier.semantics { liveRegion = LiveRegionMode.Polite },
    )
}

/** Choosing a home: the ones this computer knows, the ones on this network, by address or link. */
@Composable
fun HomePicker(session: AppSession, found: List<FoundHome>, probe: HomeProbe = remember { HomeProbe() }) {
    val t = LocalTexts.current
    val homes by session.homes.collectAsState()
    val scope = rememberCoroutineScope()
    var address by remember { mutableStateOf("") }
    var connecting by remember { mutableStateOf(false) }
    var problem by remember { mutableStateOf<String?>(null) }
    var renaming by remember { mutableStateOf<Home?>(null) }
    var throughCloud by remember { mutableStateOf(false) }
    if (throughCloud) {
        CloudHomes(session, onClose = { throughCloud = false })
        return
    }

    fun connect(link: ConnectLink) {
        connecting = true
        problem = null
        scope.launch {
            try {
                session.connect(link, probe)
            } catch (e: CancellationException) {
                throw e
            } catch (e: NotAHome) {
                problem = t["picker.notAHome", e.tried.joinToString(", ") { it.substringAfter("://") }]
            } catch (e: Exception) {
                problem = e.message ?: t["common.error"]
            } finally {
                connecting = false
            }
        }
    }

    Column(
        Modifier.fillMaxSize().verticalScroll(rememberScrollState()).padding(32.dp),
        horizontalAlignment = Alignment.CenterHorizontally,
    ) {
        Column(Modifier.widthIn(max = 640.dp).fillMaxWidth(), verticalArrangement = Arrangement.spacedBy(16.dp)) {
            Text(t["picker.title"], style = MaterialTheme.typography.headlineMedium, modifier = Modifier.semantics { heading() })
            if (homes.isNotEmpty()) {
                Heading(t["picker.yours"])
                for (home in homes) {
                    HomeRow(
                        home.label, home.address, enabled = !connecting, onClick = { session.use(home) },
                        actions = {
                            TextButton(onClick = { renaming = home }) { Text(t["picker.rename"]) }
                            TextButton(onClick = { session.forget(home) }) { Text(t["picker.remove"]) }
                        },
                    )
                }
                HorizontalDivider()
            }
            Heading(t["picker.network"])
            if (found.isEmpty()) {
                Text(t["picker.searching"], color = MaterialTheme.colorScheme.onSurfaceVariant)
            }
            for (f in found) {
                HomeRow(f.name, f.url.substringAfter("://"), enabled = !connecting, onClick = { session.use(Home(f.url, f.name, f.id)) })
            }
            HorizontalDivider()
            Heading(t["picker.add"])
            OutlinedTextField(
                value = address,
                onValueChange = { address = it; problem = null },
                label = { Text(t["picker.address"]) },
                supportingText = { Text(t["picker.addressHint"]) },
                singleLine = true,
                keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Uri, imeAction = ImeAction.Go),
                keyboardActions = KeyboardActions(onGo = { linkFor(address)?.let(::connect) }),
                modifier = Modifier.fillMaxWidth(),
            )
            problem?.let { Problem(it) }
            Button(onClick = { linkFor(address)?.let(::connect) }, enabled = !connecting && linkFor(address) != null) {
                Text(if (connecting) t["picker.connecting"] else t["picker.connect"])
            }
            HorizontalDivider()
            // away from home: the homes linked to a Maktaba Cloud account (story 42.7)
            OutlinedButton(onClick = { throughCloud = true }, enabled = !connecting) { Text(t["cloud.title"]) }
        }
    }
    renaming?.let { home ->
        RenameDialog(home, onDismiss = { renaming = null }, onSave = { session.rename(home, it); renaming = null })
    }
}

@Composable
private fun HomeRow(name: String, address: String, enabled: Boolean, onClick: () -> Unit, actions: @Composable () -> Unit = {}) {
    Row(Modifier.fillMaxWidth(), verticalAlignment = Alignment.CenterVertically) {
        Column(Modifier.weight(1f).clickable(enabled = enabled, onClick = onClick).padding(vertical = 8.dp)) {
            Text(name, style = MaterialTheme.typography.titleMedium)
            // an address reads left to right in either language
            Text("⁦$address⁩", style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
        }
        actions()
    }
}

@Composable
private fun RenameDialog(home: Home, onDismiss: () -> Unit, onSave: (String) -> Unit) {
    val t = LocalTexts.current
    var name by remember { mutableStateOf(home.nickname ?: "") }
    AlertDialog(
        onDismissRequest = onDismiss,
        title = { Text(t["picker.renameTitle"]) },
        text = {
            Column(verticalArrangement = Arrangement.spacedBy(8.dp)) {
                Text(t["picker.renameHint"])
                OutlinedTextField(name, { name = it }, singleLine = true, placeholder = { Text(home.name) })
            }
        },
        confirmButton = { Button(onClick = { onSave(name) }) { Text(t["common.save"]) } },
        dismissButton = { TextButton(onClick = onDismiss) { Text(t["common.cancel"]) } },
    )
}

/** A home's link the player was opened with: never followed without the person's yes. */
@Composable
fun ConnectLinkDialog(session: AppSession, link: ConnectLink, probe: HomeProbe = remember { HomeProbe() }) {
    val t = LocalTexts.current
    val scope = rememberCoroutineScope()
    var busy by remember { mutableStateOf(false) }
    var problem by remember { mutableStateOf<String?>(null) }
    AlertDialog(
        onDismissRequest = { if (!busy) session.dismissLink() },
        title = { Text(t["link.title"]) },
        text = {
            Column(verticalArrangement = Arrangement.spacedBy(8.dp)) {
                Text(t["link.body", link.name ?: link.urls.first().substringAfter("://"), link.urls.joinToString(", ") { it.substringAfter("://") }])
                problem?.let { Problem(it) }
            }
        },
        confirmButton = {
            Button(enabled = !busy, onClick = {
                busy = true
                scope.launch {
                    try {
                        session.connect(link, probe)
                    } catch (e: CancellationException) {
                        throw e
                    } catch (e: NotAHome) {
                        problem = t["picker.notAHome", e.tried.joinToString(", ") { it.substringAfter("://") }]
                    } catch (e: Exception) {
                        problem = e.message ?: t["common.error"]
                    } finally {
                        busy = false
                    }
                }
            }) { Text(t["link.open"]) }
        },
        // the safe choice first in reading order
        dismissButton = { TextButton(enabled = !busy, onClick = { session.dismissLink() }) { Text(t["common.cancel"]) } },
    )
}

/** This computer's name, as a code sign-in labels the session ("<name>"). */
fun deviceLabel(): String = runCatching { InetAddress.getLocalHost().hostName }.getOrNull()
    ?.substringBefore('.')?.takeIf { it.isNotBlank() } ?: "Maktaba Player"

/** Signing in to the chosen home: with a password, or with a code another device approves. */
@Composable
fun SignIn(session: AppSession) {
    val t = LocalTexts.current
    val home by session.current.collectAsState()
    val restoreProblem by session.restoreProblem.collectAsState()
    var tab by remember { mutableStateOf(0) }
    Column(
        Modifier.fillMaxSize().verticalScroll(rememberScrollState()).padding(32.dp),
        horizontalAlignment = Alignment.CenterHorizontally,
    ) {
        Column(Modifier.widthIn(max = 520.dp).fillMaxWidth(), verticalArrangement = Arrangement.spacedBy(16.dp)) {
            Text(
                t["signin.title", home?.label ?: ""],
                style = MaterialTheme.typography.headlineMedium,
                modifier = Modifier.semantics { heading() },
            )
            restoreProblem?.let {
                Problem(t["signin.restoreFailed", t.sentence(it)])
                TextButton(onClick = { session.retryRestore() }) { Text(t["common.retry"]) }
            }
            PrimaryTabRow(selectedTabIndex = tab) {
                Tab(tab == 0, onClick = { tab = 0 }, text = { Text(t["signin.withPassword"]) })
                Tab(tab == 1, onClick = { tab = 1 }, text = { Text(t["signin.withCode"]) })
            }
            if (tab == 0) PasswordSignIn(session) else CodeSignIn(session, onUnsupported = { tab = 0 })
            // a home chosen in Maktaba Cloud, at an address the cloud gave: no password needed
            val h = home
            if (h != null && session.cloud?.signedIn == true && h.url in h.cloudAddresses) CloudSignInButton(session)
            TextButton(onClick = { session.switchHome() }) { Text(t["signin.otherHome"]) }
        }
    }
}

@Composable
private fun PasswordSignIn(session: AppSession) {
    val t = LocalTexts.current
    val scope = rememberCoroutineScope()
    var username by remember { mutableStateOf("") }
    var password by remember { mutableStateOf("") }
    var busy by remember { mutableStateOf(false) }
    var problem by remember { mutableStateOf<String?>(null) }
    fun submit() {
        if (busy || username.isBlank() || password.isEmpty()) return
        busy = true
        problem = null
        scope.launch {
            try {
                session.signIn(username.trim(), password)
            } catch (e: CancellationException) {
                throw e
            } catch (e: ApiException.SignedOut) {
                problem = t["signin.failed"]
            } catch (e: ApiException.Status) {
                problem = if (e.code == 401) t["signin.failed"] else e.message
            } catch (e: Exception) {
                problem = e.message ?: t["common.error"]
            } finally {
                busy = false
            }
        }
    }
    Column(verticalArrangement = Arrangement.spacedBy(12.dp)) {
        OutlinedTextField(username, { username = it }, label = { Text(t["signin.username"]) }, singleLine = true, modifier = Modifier.fillMaxWidth())
        OutlinedTextField(
            password, { password = it }, label = { Text(t["signin.password"]) }, singleLine = true,
            visualTransformation = PasswordVisualTransformation(),
            keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Password, imeAction = ImeAction.Go),
            keyboardActions = KeyboardActions(onGo = { submit() }),
            modifier = Modifier.fillMaxWidth(),
        )
        problem?.let { Problem(it) }
        Button(onClick = ::submit, enabled = !busy && username.isNotBlank() && password.isNotEmpty()) {
            if (busy) CircularProgressIndicator(Modifier.width(18.dp), strokeWidth = 2.dp) else Text(t["signin.submit"])
        }
    }
}

@Composable
private fun CodeSignIn(session: AppSession, onUnsupported: () -> Unit) {
    val t = LocalTexts.current
    val home by session.current.collectAsState()
    var code by remember { mutableStateOf<DeviceCode?>(null) }
    var problem by remember { mutableStateOf<String?>(null) }
    LaunchedEffect(home?.url) {
        try {
            session.signInWithCode(deviceLabel(), onCode = { code = it; problem = null })
        } catch (e: CancellationException) {
            throw e
        } catch (e: DeviceSignIn.Unsupported) {
            problem = t["signin.codeUnsupported"]
            onUnsupported()
        } catch (e: Exception) {
            problem = e.message ?: t["common.error"]
        }
    }
    Column(horizontalAlignment = Alignment.CenterHorizontally, verticalArrangement = Arrangement.spacedBy(12.dp), modifier = Modifier.fillMaxWidth()) {
        Text(t["signin.codeHint"], textAlign = TextAlign.Center)
        val c = code
        if (c == null) {
            if (problem == null) CircularProgressIndicator()
        } else {
            // the code reads left to right in either language
            Box(Modifier.semantics { liveRegion = LiveRegionMode.Polite }) {
                Text("⁦${PairingCode.shown(c.code)}⁩", style = MaterialTheme.typography.displayMedium.copy(fontSize = 56.sp, letterSpacing = 4.sp))
            }
            val home0 = home
            val verify = c.verifyUrl ?: home0?.let { c.approveUrl(it.httpUrl) }
            if (verify != null) Qr(verify, t["signin.withCode"])
            Spacer(Modifier.padding(4.dp))
            Text(t["signin.codeRenew"], style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
        }
        problem?.let { Problem(it) }
    }
}
