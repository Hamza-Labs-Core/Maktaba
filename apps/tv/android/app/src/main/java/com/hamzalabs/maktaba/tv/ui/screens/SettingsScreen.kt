package com.hamzalabs.maktaba.tv.ui.screens

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.input.ImeAction
import androidx.compose.ui.text.input.PasswordVisualTransformation
import androidx.compose.ui.unit.dp
import androidx.tv.material3.ExperimentalTvMaterial3Api
import androidx.tv.material3.MaterialTheme
import androidx.tv.material3.Text
import com.hamzalabs.maktaba.tv.data.SettingsStore
import com.hamzalabs.maktaba.tv.data.repository.MediaRepository
import com.hamzalabs.maktaba.tv.ui.components.MaktabaButton
import com.hamzalabs.maktaba.tv.ui.components.SectionTitle
import com.hamzalabs.maktaba.tv.ui.components.TvTextField
import com.hamzalabs.maktaba.tv.ui.theme.MaktabaPalette
import kotlinx.coroutines.launch

/**
 * Server connection + sign-in + preferences. When unauthenticated this
 * is the onboarding screen (RootNav routes here); once signed in it
 * shows the language toggle and sign-out.
 */
@OptIn(ExperimentalTvMaterial3Api::class)
@Composable
fun SettingsScreen(
    repository: MediaRepository,
    settings: SettingsStore,
    onSignedIn: () -> Unit,
) {
    var server by remember { mutableStateOf(settings.serverUrl) }
    var username by remember { mutableStateOf("") }
    var password by remember { mutableStateOf("") }
    var language by remember { mutableStateOf(settings.language) }
    var status by remember { mutableStateOf<String?>(null) }
    val scope = rememberCoroutineScope()

    fun applyServer() {
        settings.serverUrl = server.trim()
        repository.rebuild()
    }

    Column(
        modifier = Modifier.fillMaxSize().padding(64.dp),
        verticalArrangement = Arrangement.spacedBy(20.dp),
    ) {
        SectionTitle("Settings")

        TvTextField(
            value = server,
            onValueChange = { server = it },
            label = "Server URL",
            modifier = Modifier.fillMaxWidth(0.6f),
            keyboardOptions = KeyboardOptions(imeAction = ImeAction.Next),
        )
        MaktabaButton(onClick = { applyServer() }) { Text("Save Server") }

        if (!repository.isLoggedIn) {
            Text("Sign In", style = MaterialTheme.typography.titleLarge, color = MaktabaPalette.Text)
            TvTextField(
                value = username,
                onValueChange = { username = it },
                label = "Username",
                modifier = Modifier.fillMaxWidth(0.6f),
            )
            TvTextField(
                value = password,
                onValueChange = { password = it },
                label = "Password",
                visualTransformation = PasswordVisualTransformation(),
                modifier = Modifier.fillMaxWidth(0.6f),
            )
            status?.let { Text(it, color = MaktabaPalette.Gold) }
            MaktabaButton(onClick = {
                applyServer()
                scope.launch {
                    repository.login(username, password)
                        .onSuccess { status = null; onSignedIn() }
                        .onFailure { status = it.message ?: "Sign-in failed" }
                }
            }) { Text("Sign In") }
        } else {
            MaktabaButton(onClick = { repository.logout() }) { Text("Sign Out") }
        }

        Text("Language", style = MaterialTheme.typography.titleLarge, color = MaktabaPalette.Text)
        Row(horizontalArrangement = Arrangement.spacedBy(16.dp)) {
            MaktabaButton(onClick = { language = "en"; settings.language = "en" }) { Text("English") }
            MaktabaButton(onClick = { language = "ar"; settings.language = "ar" }) { Text("العربية") }
        }
        Text(
            "Current: $language",
            style = MaterialTheme.typography.labelLarge,
            color = MaktabaPalette.Muted,
        )
    }
}
