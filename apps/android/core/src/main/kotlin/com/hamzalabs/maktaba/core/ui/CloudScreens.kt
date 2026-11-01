package com.hamzalabs.maktaba.core.ui

import androidx.activity.compose.BackHandler
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.text.KeyboardActions
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.outlined.Cloud
import androidx.compose.material3.Button
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.semantics.LiveRegionMode
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.liveRegion
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.input.ImeAction
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.text.input.PasswordVisualTransformation
import androidx.compose.ui.unit.dp
import com.hamzalabs.maktaba.core.R
import com.hamzalabs.maktaba.core.api.ApiException
import com.hamzalabs.maktaba.core.session.ConnectProgress
import com.hamzalabs.maktaba.core.session.AppSession
import com.hamzalabs.maktaba.core.session.CloudException
import com.hamzalabs.maktaba.core.session.CloudHome
import com.hamzalabs.maktaba.core.session.CloudSession
import com.hamzalabs.maktaba.core.session.NotAHome
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.launch

/**
 * Through Maktaba Cloud (story 42.7): sign in to the cloud, see the homes linked to that
 * account, and open one: the app reaches it at its own address or through the relay, and the
 * home signs the person in with a one-use assertion from the cloud. The cloud says who the
 * person is; the home decides what they may do. The cloud's sign-in lasts until the app quits.
 */
@Composable
fun CloudHomes(app: MaktabaApp, session: AppSession, onClose: () -> Unit) {
    BackHandler(onBack = onClose)
    var signedIn by remember { mutableStateOf(session.cloud?.signedIn == true) }
    StartFrame(app) {
        Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(12.dp)) {
            Icon(Icons.Outlined.Cloud, null, tint = MaterialTheme.colorScheme.primary)
            Text(stringResource(R.string.cloud_title), style = MaterialTheme.typography.titleLarge, modifier = Modifier.weight(1f))
            TextButton(onClick = onClose) { Text(stringResource(R.string.close)) }
        }
        if (signedIn) {
            CloudHomeList(session, onSignedOut = { signedIn = false })
        } else {
            CloudSignIn(session, onSignedIn = { signedIn = true })
        }
    }
}

@Composable
private fun CloudSignIn(session: AppSession, onSignedIn: () -> Unit) {
    var email by rememberSaveable { mutableStateOf("") }
    var password by remember { mutableStateOf("") }
    var advanced by rememberSaveable { mutableStateOf(false) }
    var api by rememberSaveable { mutableStateOf(session.cloudApi.takeIf { it != CloudSession.DEFAULT_API } ?: "") }
    var busy by remember { mutableStateOf(false) }
    var problem by remember { mutableStateOf<String?>(null) }
    val scope = rememberCoroutineScope()
    val wrong = stringResource(R.string.cloud_wrong)
    val tooMany = stringResource(R.string.cloud_too_many)
    val badAddress = stringResource(R.string.cloud_address_invalid)

    fun signIn() {
        if (busy || email.isBlank() || password.isEmpty()) return
        val base = if (api.isBlank()) CloudSession.normalize(CloudSession.DEFAULT_API) else CloudSession.normalize(api)
        if (base == null) {
            problem = badAddress
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
                    400, 401 -> wrong
                    429 -> tooMany
                    else -> e.message
                }
            } catch (e: Exception) {
                problem = e.message
            } finally {
                busy = false
            }
        }
    }

    Text(stringResource(R.string.cloud_intro), style = MaterialTheme.typography.bodyMedium)
    OutlinedTextField(
        value = email,
        onValueChange = { email = it; problem = null },
        label = { Text(stringResource(R.string.cloud_email)) },
        singleLine = true,
        enabled = !busy,
        keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Email, imeAction = ImeAction.Next, autoCorrectEnabled = false),
        modifier = Modifier.fillMaxWidth().testTag("cloud-email"),
    )
    OutlinedTextField(
        value = password,
        onValueChange = { password = it; problem = null },
        label = { Text(stringResource(R.string.password)) },
        singleLine = true,
        enabled = !busy,
        visualTransformation = PasswordVisualTransformation(),
        keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Password, imeAction = ImeAction.Go),
        keyboardActions = KeyboardActions(onGo = { signIn() }),
        modifier = Modifier.fillMaxWidth().testTag("cloud-password"),
    )
    if (advanced) {
        OutlinedTextField(
            value = api,
            onValueChange = { api = it; problem = null },
            label = { Text(stringResource(R.string.cloud_address)) },
            placeholder = { Text(CloudSession.DEFAULT_API.substringAfter("://")) },
            supportingText = { Text(stringResource(R.string.cloud_address_hint)) },
            singleLine = true,
            enabled = !busy,
            keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Uri, autoCorrectEnabled = false),
            modifier = Modifier.fillMaxWidth(),
        )
    } else {
        TextButton(onClick = { advanced = true }) { Text(stringResource(R.string.cloud_advanced)) }
    }
    Button(onClick = ::signIn, enabled = !busy && email.isNotBlank() && password.isNotEmpty(), modifier = Modifier.fillMaxWidth().testTag("cloud-sign-in")) {
        Text(stringResource(if (busy) R.string.progress_signing_in_button else R.string.cloud_sign_in))
    }
    problem?.let { Problem(it) }
}

@Composable
private fun CloudHomeList(session: AppSession, onSignedOut: () -> Unit) {
    val cloud = session.cloud ?: return onSignedOut()
    var homes by remember { mutableStateOf<List<CloudHome>?>(null) }
    var problem by remember { mutableStateOf<String?>(null) }
    var opening by remember { mutableStateOf<String?>(null) }
    var reload by remember { mutableIntStateOf(0) }
    val progress by session.progress.collectAsState()
    val scope = rememberCoroutineScope()
    val unreachable = stringResource(R.string.cloud_home_unreachable)
    val refused = stringResource(R.string.cloud_home_refused)
    val locked = stringResource(R.string.cloud_home_locked)
    val changePassword = stringResource(R.string.cloud_home_change_password)

    LaunchedEffect(reload) {
        problem = null
        try {
            homes = cloud.homes()
        } catch (e: CancellationException) {
            throw e
        } catch (e: CloudException) {
            if (!cloud.signedIn) onSignedOut() else problem = e.message
        } catch (e: Exception) {
            problem = e.message
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
                problem = unreachable
            } catch (e: ApiException.Status) {
                problem = when (e.code) {
                    423 -> locked
                    403 -> changePassword
                    else -> refused
                }
            } catch (e: CloudException) {
                if (!cloud.signedIn) onSignedOut() else problem = e.message
            } catch (e: Exception) {
                problem = e.message
            } finally {
                opening = null
            }
        }
    }

    cloud.user?.email?.let {
        Text(stringResource(R.string.cloud_signed_in_as, it), style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
    }
    Text(stringResource(R.string.cloud_your_homes), style = MaterialTheme.typography.titleMedium)
    val list = homes
    when {
        list == null && problem == null -> StatusLine(stringResource(R.string.progress_cloud_loading))
        list != null && list.isEmpty() -> Text(stringResource(R.string.cloud_no_homes), style = MaterialTheme.typography.bodyMedium)
    }
    list?.forEach { home ->
        Row(
            Modifier.fillMaxWidth()
                .clickable(enabled = home.linked && opening == null, role = Role.Button) { open(home) }
                .padding(vertical = 10.dp)
                .testTag("cloud-home-${home.slug}"),
            verticalAlignment = Alignment.CenterVertically,
            horizontalArrangement = Arrangement.spacedBy(12.dp),
        ) {
            Column(Modifier.weight(1f)) {
                Text(home.name.ifBlank { home.slug }, style = MaterialTheme.typography.titleMedium)
                Text(
                    stringResource(if (home.online) R.string.cloud_online else R.string.cloud_offline) +
                        if (home.linked) "" else " · " + stringResource(R.string.cloud_not_linked),
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
            }
        }
        HorizontalDivider(color = MaterialTheme.colorScheme.outlineVariant)
        // opening this home: finding it (its own address, then the relay), found, signing in
        val step = progress
        if (opening == home.serverId && step != null && step !is ConnectProgress.Failed) ConnectStatus(step, session.now)
    }
    if (list?.any { !it.linked } == true) {
        Text(stringResource(R.string.cloud_how_to_link), style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
    }
    problem?.let { Problem(it) }
    Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
        OutlinedButton(onClick = { homes = null; reload++ }, enabled = opening == null) { Text(stringResource(R.string.cloud_refresh)) }
        TextButton(onClick = { cloud.signOut(); session.cloud = null; onSignedOut() }, enabled = opening == null) {
            Text(stringResource(R.string.cloud_sign_out))
        }
    }
}

/**
 * On the sign-in screen of a home chosen in Maktaba Cloud (at an address the cloud gave for
 * it): sign in there through the cloud, without a password.
 */
@Composable
internal fun CloudSignInButton(session: AppSession) {
    var busy by remember { mutableStateOf(false) }
    var problem by remember { mutableStateOf<String?>(null) }
    val scope = rememberCoroutineScope()
    val refused = stringResource(R.string.cloud_home_refused)
    OutlinedButton(
        onClick = {
            busy = true
            problem = null
            scope.launch {
                try {
                    session.signInWithCloud()
                } catch (e: CancellationException) {
                    throw e
                } catch (e: ApiException.Status) {
                    problem = refused
                } catch (e: Exception) {
                    problem = e.message
                } finally {
                    busy = false
                }
            }
        },
        enabled = !busy,
        modifier = Modifier.fillMaxWidth().testTag("signin-through-cloud"),
    ) {
        Icon(Icons.Outlined.Cloud, null, Modifier.size(18.dp))
        Text("  " + stringResource(R.string.cloud_sign_in_here))
    }
    problem?.let { Problem(it) }
}

@Composable
private fun Problem(text: String) {
    Text(
        text,
        color = MaterialTheme.colorScheme.error,
        style = MaterialTheme.typography.bodyMedium,
        modifier = Modifier.semantics { liveRegion = LiveRegionMode.Polite },
    )
}
