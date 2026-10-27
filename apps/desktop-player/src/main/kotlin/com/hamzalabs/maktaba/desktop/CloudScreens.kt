package com.hamzalabs.maktaba.desktop

import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.text.KeyboardActions
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.Button
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.heading
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.input.ImeAction
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.text.input.PasswordVisualTransformation
import androidx.compose.ui.unit.dp
import com.hamzalabs.maktaba.core.api.ApiException
import com.hamzalabs.maktaba.core.session.AppSession
import com.hamzalabs.maktaba.core.session.CloudException
import com.hamzalabs.maktaba.core.session.CloudHome
import com.hamzalabs.maktaba.core.session.CloudSession
import com.hamzalabs.maktaba.core.session.NotAHome
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.launch

/**
 * Through Maktaba Cloud (story 42.7), as on the phone: sign in to the cloud, see the homes
 * linked to that account, open one (at its own address when it answers, else through the
 * relay; the home signs the person in with a one-use assertion and decides what they may do).
 * The cloud's sign-in lasts until the player quits.
 */
@Composable
fun CloudHomes(session: AppSession, onClose: () -> Unit) {
    val t = LocalTexts.current
    var signedIn by remember { mutableStateOf(session.cloud?.signedIn == true) }
    Column(
        Modifier.fillMaxSize().verticalScroll(rememberScrollState()).padding(32.dp),
        horizontalAlignment = Alignment.CenterHorizontally,
    ) {
        Column(Modifier.widthIn(max = 640.dp).fillMaxWidth(), verticalArrangement = Arrangement.spacedBy(16.dp)) {
            Row(verticalAlignment = Alignment.CenterVertically) {
                Text(
                    t["cloud.title"], style = MaterialTheme.typography.headlineMedium,
                    modifier = Modifier.weight(1f).semantics { heading() },
                )
                TextButton(onClick = onClose) { Text(t["common.close"]) }
            }
            if (signedIn) CloudHomeList(session, onSignedOut = { signedIn = false })
            else CloudSignIn(session, onSignedIn = { signedIn = true })
        }
    }
}

@Composable
private fun CloudSignIn(session: AppSession, onSignedIn: () -> Unit) {
    val t = LocalTexts.current
    val scope = rememberCoroutineScope()
    var email by remember { mutableStateOf("") }
    var password by remember { mutableStateOf("") }
    var advanced by remember { mutableStateOf(false) }
    var api by remember { mutableStateOf(session.cloudApi.takeIf { it != CloudSession.DEFAULT_API } ?: "") }
    var busy by remember { mutableStateOf(false) }
    var problem by remember { mutableStateOf<String?>(null) }

    fun signIn() {
        if (busy || email.isBlank() || password.isEmpty()) return
        val base = CloudSession.normalize(api.ifBlank { CloudSession.DEFAULT_API })
        if (base == null) {
            problem = t["cloud.addressInvalid"]
            return
        }
        busy = true
        problem = null
        scope.launch {
            try {
                val c = CloudSession(base)
                c.signIn(email.trim(), password)
                session.cloudApi = base.toString().trimEnd('/')
                session.cloud = c
                onSignedIn()
            } catch (e: CancellationException) {
                throw e
            } catch (e: CloudException) {
                problem = when (e.status) {
                    400, 401 -> t["cloud.wrong"]
                    429 -> t["cloud.tooMany"]
                    else -> e.message
                }
            } catch (e: Exception) {
                problem = e.message ?: t["common.error"]
            } finally {
                busy = false
            }
        }
    }

    Text(t["cloud.intro"])
    OutlinedTextField(email, { email = it; problem = null }, label = { Text(t["cloud.email"]) }, singleLine = true,
        keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Email, imeAction = ImeAction.Next), modifier = Modifier.fillMaxWidth())
    OutlinedTextField(
        password, { password = it; problem = null }, label = { Text(t["signin.password"]) }, singleLine = true,
        visualTransformation = PasswordVisualTransformation(),
        keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Password, imeAction = ImeAction.Go),
        keyboardActions = KeyboardActions(onGo = { signIn() }),
        modifier = Modifier.fillMaxWidth(),
    )
    if (advanced) {
        OutlinedTextField(
            api, { api = it; problem = null }, label = { Text(t["cloud.address"]) },
            placeholder = { Text(CloudSession.DEFAULT_API.substringAfter("://")) },
            supportingText = { Text(t["cloud.addressHint"]) }, singleLine = true, modifier = Modifier.fillMaxWidth(),
        )
    } else {
        TextButton(onClick = { advanced = true }) { Text(t["cloud.advanced"]) }
    }
    Button(onClick = ::signIn, enabled = !busy && email.isNotBlank() && password.isNotEmpty()) {
        if (busy) CircularProgressIndicator(Modifier.width(18.dp), strokeWidth = 2.dp) else Text(t["cloud.signIn"])
    }
    problem?.let { Problem(it) }
}

@Composable
private fun CloudHomeList(session: AppSession, onSignedOut: () -> Unit) {
    val t = LocalTexts.current
    val cloud = session.cloud
    if (cloud == null) {
        onSignedOut()
        return
    }
    val scope = rememberCoroutineScope()
    var homes by remember { mutableStateOf<List<CloudHome>?>(null) }
    var problem by remember { mutableStateOf<String?>(null) }
    var opening by remember { mutableStateOf<String?>(null) }
    var reload by remember { mutableIntStateOf(0) }

    LaunchedEffect(reload) {
        problem = null
        try {
            homes = cloud.homes()
        } catch (e: CancellationException) {
            throw e
        } catch (e: Exception) {
            if (!cloud.signedIn) onSignedOut() else problem = e.message ?: t["common.error"]
        }
    }

    fun open(home: CloudHome) {
        if (opening != null || !home.linked) return
        opening = home.serverId
        problem = null
        scope.launch {
            try {
                session.useCloudHome(home)
            } catch (e: CancellationException) {
                throw e
            } catch (e: NotAHome) {
                problem = t["cloud.unreachable"]
            } catch (e: ApiException.Status) {
                problem = when (e.code) {
                    423 -> t["cloud.locked"]
                    403 -> t["cloud.changePassword"]
                    else -> t["cloud.refused"]
                }
            } catch (e: Exception) {
                if (!cloud.signedIn) onSignedOut() else problem = e.message ?: t["common.error"]
            } finally {
                opening = null
            }
        }
    }

    cloud.user?.email?.let { Text(t["cloud.signedInAs", it], style = MaterialTheme.typography.bodySmall) }
    Heading(t["cloud.yourHomes"])
    val list = homes
    if (list == null && problem == null) CircularProgressIndicator()
    if (list != null && list.isEmpty()) Text(t["cloud.noHomes"])
    list?.forEach { home ->
        Row(
            Modifier.fillMaxWidth().clickable(enabled = home.linked && opening == null, role = Role.Button) { open(home) }.padding(vertical = 8.dp),
            verticalAlignment = Alignment.CenterVertically,
        ) {
            Column(Modifier.weight(1f)) {
                Text(home.name.ifBlank { home.slug }, style = MaterialTheme.typography.titleMedium)
                Text(
                    (if (home.online) t["cloud.online"] else t["cloud.offline"]) + if (home.linked) "" else " · " + t["cloud.notLinked"],
                    style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
            }
            if (opening == home.serverId) CircularProgressIndicator(Modifier.width(18.dp), strokeWidth = 2.dp)
        }
        HorizontalDivider()
    }
    if (list?.any { !it.linked } == true) {
        Text(t["cloud.howToLink"], style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
    }
    problem?.let { Problem(it) }
    Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
        OutlinedButton(onClick = { homes = null; reload++ }, enabled = opening == null) { Text(t["cloud.refresh"]) }
        TextButton(onClick = { cloud.signOut(); session.cloud = null; onSignedOut() }, enabled = opening == null) { Text(t["cloud.signOut"]) }
    }
}

/** On the sign-in screen of a home chosen in Maktaba Cloud, at an address the cloud gave: no password. */
@Composable
fun CloudSignInButton(session: AppSession) {
    val t = LocalTexts.current
    val scope = rememberCoroutineScope()
    var busy by remember { mutableStateOf(false) }
    var problem by remember { mutableStateOf<String?>(null) }
    OutlinedButton(enabled = !busy, onClick = {
        busy = true
        problem = null
        scope.launch {
            try {
                session.signInWithCloud()
            } catch (e: CancellationException) {
                throw e
            } catch (e: ApiException.Status) {
                problem = t["cloud.refused"]
            } catch (e: Exception) {
                problem = e.message ?: t["common.error"]
            } finally {
                busy = false
            }
        }
    }) { Text(t["cloud.signInHere"]) }
    problem?.let { Problem(it) }
}
