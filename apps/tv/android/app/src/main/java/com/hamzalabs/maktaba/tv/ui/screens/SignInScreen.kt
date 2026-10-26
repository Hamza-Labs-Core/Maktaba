package com.hamzalabs.maktaba.tv.ui.screens

import android.os.Build
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.text.KeyboardActions
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.focus.FocusRequester
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.input.ImeAction
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.text.input.PasswordVisualTransformation
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.tv.material3.MaterialTheme
import androidx.tv.material3.Text
import com.hamzalabs.maktaba.core.api.ApiException
import com.hamzalabs.maktaba.core.api.AuthTokens
import com.hamzalabs.maktaba.core.session.Home
import com.hamzalabs.maktaba.tv.MaktabaTVApp
import com.hamzalabs.maktaba.tv.R
import com.hamzalabs.maktaba.tv.data.DeviceCode
import com.hamzalabs.maktaba.tv.data.DeviceSignIn
import com.hamzalabs.maktaba.tv.ui.components.LibraryBackdrop
import com.hamzalabs.maktaba.tv.ui.components.MaktabaButton
import com.hamzalabs.maktaba.tv.ui.components.QrCode
import com.hamzalabs.maktaba.tv.ui.components.ReadingPanel
import com.hamzalabs.maktaba.tv.ui.components.SectionTitle
import com.hamzalabs.maktaba.tv.ui.components.TvTextField
import com.hamzalabs.maktaba.tv.ui.theme.MaktabaPalette
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.launch
import com.hamzalabs.maktaba.core.R as CoreR

/**
 * Signing the TV in to [home]. First choice: a code. The TV shows a short code and a QR of the
 * home's approve page (`<home>/link?code=…`); a person signed in on their phone or the web
 * approves it, and the TV is signed in as them, no password typed with a remote. Fallback: the
 * username and password on the on-screen keyboard (a home without sign-in by code goes straight
 * there).
 */
@Composable
fun SignInScreen(app: MaktabaTVApp, home: Home) {
    var withPassword by remember(home.url) { mutableStateOf(false) }
    var unsupported by remember(home.url) { mutableStateOf(false) }

    LibraryBackdrop {
        Column(
            Modifier.fillMaxSize().padding(horizontal = 80.dp, vertical = 48.dp),
            verticalArrangement = Arrangement.Center,
        ) {
            if (withPassword) {
                PasswordSignIn(app, home, unsupported) { withPassword = false }
            } else {
                CodeSignIn(app, home, onPassword = { withPassword = true }, onUnsupported = {
                    unsupported = true
                    withPassword = true
                })
            }
        }
    }
}

@Composable
private fun CodeSignIn(app: MaktabaTVApp, home: Home, onPassword: () -> Unit, onUnsupported: () -> Unit) {
    val label = stringResource(R.string.tv_label).let { base ->
        val model = Build.MODEL?.takeIf { it.isNotBlank() }
        if (model != null) "$base ($model)" else base
    }
    var code by remember { mutableStateOf<DeviceCode?>(null) }
    var renewed by remember { mutableStateOf(false) }
    var problem by remember { mutableStateOf<String?>(null) }
    var attempt by remember { mutableStateOf(0) }
    val focus = remember { FocusRequester() }

    LaunchedEffect(home.url, attempt) {
        problem = null
        try {
            val tokens = DeviceSignIn(home.httpUrl, label).run { c ->
                renewed = code != null
                code = c
            }
            app.tokens(home.url).save(AuthTokens(tokens.accessToken, tokens.refreshToken))
            app.session.use(home) // signed in at once: the session finds the tokens kept
        } catch (e: CancellationException) {
            throw e
        } catch (e: DeviceSignIn.Unsupported) {
            onUnsupported()
        } catch (e: Exception) {
            problem = e.message ?: e.toString()
        }
    }
    LaunchedEffect(Unit) { runCatching { focus.requestFocus() } }

    Row(horizontalArrangement = Arrangement.spacedBy(48.dp), verticalAlignment = Alignment.CenterVertically) {
        ReadingPanel(Modifier.weight(1f), contentPadding = 32.dp) {
            SectionTitle(stringResource(R.string.signin_code_title, home.name))
            val c = code
            when {
                problem != null -> {
                    Text(
                        stringResource(CoreR.string.signin_unreachable, home.name) + "\n" + problem,
                        style = MaterialTheme.typography.bodyLarge,
                        color = MaktabaPalette.Gold,
                        modifier = Modifier.padding(vertical = 16.dp),
                    )
                }
                c == null -> Text(stringResource(R.string.loading), style = MaterialTheme.typography.bodyLarge, color = MaktabaPalette.Muted)
                else -> {
                    Text(
                        stringResource(R.string.signin_code_step1),
                        style = MaterialTheme.typography.bodyLarge,
                        color = MaktabaPalette.Muted,
                        modifier = Modifier.padding(top = 16.dp),
                    )
                    Text(c.verificationUri, style = MaterialTheme.typography.titleLarge, color = MaktabaPalette.Text)
                    Text(stringResource(R.string.signin_code_step2), style = MaterialTheme.typography.bodyLarge, color = MaktabaPalette.Muted)
                    Text(
                        c.userCode,
                        style = MaterialTheme.typography.displayMedium.copy(letterSpacing = 6.sp),
                        color = MaktabaPalette.Gold,
                        modifier = Modifier.padding(vertical = 16.dp),
                    )
                    Text(
                        stringResource(if (renewed) R.string.signin_code_new else R.string.signin_code_waiting),
                        style = MaterialTheme.typography.labelLarge,
                        color = MaktabaPalette.Faint,
                    )
                }
            }
            Row(Modifier.padding(top = 24.dp), horizontalArrangement = Arrangement.spacedBy(16.dp)) {
                if (problem != null) {
                    MaktabaButton(onClick = { attempt++ }, modifier = Modifier.focusRequester(focus)) {
                        Text(stringResource(R.string.try_again))
                    }
                }
                MaktabaButton(
                    onClick = onPassword,
                    modifier = if (problem == null) Modifier.focusRequester(focus) else Modifier,
                ) { Text(stringResource(R.string.signin_with_password)) }
                MaktabaButton(onClick = { app.session.switchHome() }) { Text(stringResource(CoreR.string.other_home)) }
            }
        }
        code?.let { QrCode(it.verificationUriComplete, size = 280.dp) }
    }
}

@Composable
private fun PasswordSignIn(app: MaktabaTVApp, home: Home, unsupported: Boolean, onCode: () -> Unit) {
    var username by remember { mutableStateOf("") }
    var password by remember { mutableStateOf("") }
    var problem by remember { mutableStateOf<String?>(null) }
    var busy by remember { mutableStateOf(false) }
    val scope = rememberCoroutineScope()
    val first = remember { FocusRequester() }
    val passwordField = remember { FocusRequester() }
    val wrong = stringResource(CoreR.string.signin_wrong)
    val unreachable = stringResource(CoreR.string.signin_unreachable, home.name)

    LaunchedEffect(Unit) { runCatching { first.requestFocus() } }

    fun signIn() {
        if (busy || username.isBlank() || password.isEmpty()) return
        busy = true
        problem = null
        scope.launch {
            try {
                app.session.signIn(username.trim(), password)
            } catch (e: CancellationException) {
                throw e
            } catch (e: ApiException.Status) {
                problem = if (e.code == 401) wrong else e.message
            } catch (e: ApiException.Transport) {
                problem = unreachable
            } catch (e: Exception) {
                problem = e.message ?: e.toString()
            } finally {
                busy = false
            }
        }
    }

    ReadingPanel(Modifier.width(640.dp), contentPadding = 32.dp) {
        SectionTitle(stringResource(CoreR.string.signin_to, home.name))
        if (unsupported) {
            Text(
                stringResource(R.string.signin_code_unsupported),
                style = MaterialTheme.typography.bodyMedium,
                color = MaktabaPalette.Muted,
                modifier = Modifier.padding(top = 8.dp),
            )
        }
        TvTextField(
            value = username,
            onValueChange = { username = it },
            label = stringResource(CoreR.string.username),
            modifier = Modifier.fillMaxWidth().padding(top = 20.dp).focusRequester(first),
            keyboardOptions = KeyboardOptions(imeAction = ImeAction.Next, autoCorrectEnabled = false),
            keyboardActions = KeyboardActions(onNext = { runCatching { passwordField.requestFocus() } }),
        )
        TvTextField(
            value = password,
            onValueChange = { password = it },
            label = stringResource(CoreR.string.password),
            visualTransformation = PasswordVisualTransformation(),
            modifier = Modifier.fillMaxWidth().padding(top = 16.dp).focusRequester(passwordField),
            keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Password, imeAction = ImeAction.Done),
            keyboardActions = KeyboardActions(onDone = { signIn() }),
        )
        problem?.let { Text(it, color = MaktabaPalette.Gold, modifier = Modifier.padding(top = 12.dp)) }
        Row(Modifier.padding(top = 24.dp), horizontalArrangement = Arrangement.spacedBy(16.dp)) {
            MaktabaButton(onClick = ::signIn, enabled = !busy) { Text(stringResource(CoreR.string.signin_submit)) }
            if (!unsupported) MaktabaButton(onClick = onCode) { Text(stringResource(R.string.signin_with_code)) }
            MaktabaButton(onClick = { app.session.switchHome() }) { Text(stringResource(CoreR.string.other_home)) }
        }
    }
}
