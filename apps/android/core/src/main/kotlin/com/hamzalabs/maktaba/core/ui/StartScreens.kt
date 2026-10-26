package com.hamzalabs.maktaba.core.ui

import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.imePadding
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.text.KeyboardActions
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Delete
import androidx.compose.material.icons.filled.Home
import androidx.compose.material3.Button
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.ListItem
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.material3.TopAppBar
import androidx.compose.runtime.Composable
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.input.ImeAction
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.text.input.PasswordVisualTransformation
import androidx.compose.ui.unit.dp
import com.hamzalabs.maktaba.core.api.ApiClient
import com.hamzalabs.maktaba.core.api.User
import com.hamzalabs.maktaba.core.session.AppSession
import com.hamzalabs.maktaba.core.session.Home
import kotlinx.coroutines.launch

/** What every app opens with: choose a home, sign in, then the app's own content. */
@Composable
fun SessionGate(
    session: AppSession,
    title: String,
    content: @Composable (user: User, client: ApiClient) -> Unit,
) {
    when (val phase = session.phase.collectAsState().value) {
        AppSession.Phase.ChoosingHome -> HomePicker(session, title)
        AppSession.Phase.SigningIn -> SignIn(session)
        is AppSession.Phase.SignedIn -> content(phase.user, phase.client)
    }
}

/** A centred column no wider than a form should be. */
@Composable
private fun FormColumn(padding: androidx.compose.foundation.layout.PaddingValues, content: @Composable () -> Unit) {
    Box(Modifier.fillMaxSize().padding(padding).imePadding(), contentAlignment = Alignment.TopCenter) {
        Column(
            Modifier.widthIn(max = 520.dp).fillMaxWidth().verticalScroll(rememberScrollState()).padding(16.dp),
            verticalArrangement = Arrangement.spacedBy(12.dp),
        ) { content() }
    }
}

/** The homes this app knows, and a new one by its address. */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun HomePicker(session: AppSession, title: String) {
    val homes by session.homes.collectAsState()
    var address by remember { mutableStateOf("") }
    var problem by remember { mutableStateOf<String?>(null) }
    fun add() {
        val url = Home.normalize(address)
        if (url == null) {
            problem = "That is not a home's address."
            return
        }
        problem = null
        session.use(Home(url, url.substringAfter("://")))
    }
    Scaffold(topBar = { TopAppBar(title = { Text(title) }) }) { padding ->
        FormColumn(padding) {
            if (homes.isNotEmpty()) {
                Text("Your homes", style = MaterialTheme.typography.titleSmall, color = MaterialTheme.colorScheme.primary)
                homes.forEach { home ->
                    ListItem(
                        headlineContent = { Text(home.name) },
                        supportingContent = { Text(home.url.substringAfter("://")) },
                        leadingContent = { Icon(Icons.Filled.Home, null) },
                        trailingContent = {
                            IconButton(onClick = { session.forget(home) }) {
                                Icon(Icons.Filled.Delete, "Forget ${home.name}")
                            }
                        },
                        modifier = Modifier.clickable { session.use(home) },
                    )
                }
                HorizontalDivider()
            }
            Text("Add a home", style = MaterialTheme.typography.titleSmall, color = MaterialTheme.colorScheme.primary)
            OutlinedTextField(
                value = address,
                onValueChange = { address = it },
                label = { Text("Address, like 192.168.1.20:8088") },
                singleLine = true,
                keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Uri, imeAction = ImeAction.Go, autoCorrectEnabled = false),
                keyboardActions = KeyboardActions(onGo = { add() }),
                isError = problem != null,
                supportingText = problem?.let { { Text(it) } },
                modifier = Modifier.fillMaxWidth(),
            )
            Button(onClick = ::add, enabled = address.isNotBlank(), modifier = Modifier.fillMaxWidth()) { Text("Connect") }
        }
    }
}

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun SignIn(session: AppSession) {
    val home by session.current.collectAsState()
    var username by remember { mutableStateOf("") }
    var password by remember { mutableStateOf("") }
    var busy by remember { mutableStateOf(false) }
    var problem by remember { mutableStateOf<String?>(null) }
    val scope = rememberCoroutineScope()
    fun signIn() {
        if (username.isEmpty() || password.isEmpty() || busy) return
        busy = true
        problem = null
        scope.launch {
            try {
                session.signIn(username, password)
            } catch (e: Exception) {
                problem = e.message
            } finally {
                busy = false
            }
        }
    }
    Scaffold(
        topBar = {
            TopAppBar(
                title = { Text("Sign in") },
                actions = { TextButton(onClick = session::switchHome) { Text("Homes") } },
            )
        },
    ) { padding ->
        FormColumn(padding) {
            Text(home?.name ?: "", style = MaterialTheme.typography.titleMedium)
            OutlinedTextField(
                value = username,
                onValueChange = { username = it },
                label = { Text("Username") },
                singleLine = true,
                keyboardOptions = KeyboardOptions(imeAction = ImeAction.Next, autoCorrectEnabled = false),
                modifier = Modifier.fillMaxWidth(),
            )
            OutlinedTextField(
                value = password,
                onValueChange = { password = it },
                label = { Text("Password") },
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
                modifier = Modifier.fillMaxWidth().semantics { contentDescription = "Sign in" },
            ) {
                if (busy) CircularProgressIndicator(Modifier.size(18.dp), strokeWidth = 2.dp) else Text("Sign in")
            }
            Spacer(Modifier.height(8.dp))
        }
    }
}
