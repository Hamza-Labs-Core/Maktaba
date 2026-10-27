package com.hamzalabs.maktaba.core.ui

import android.graphics.BitmapFactory
import android.util.LruCache
import androidx.activity.ComponentActivity
import androidx.activity.SystemBarStyle
import androidx.activity.compose.LocalActivity
import androidx.activity.enableEdgeToEdge
import androidx.compose.foundation.Image
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.aspectRatio
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.AccountCircle
import androidx.compose.material.icons.filled.Check
import androidx.compose.material.icons.outlined.Movie
import androidx.compose.material3.DropdownMenu
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.SideEffect
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.ImageBitmap
import androidx.compose.ui.graphics.asImageBitmap
import androidx.compose.ui.graphics.toArgb
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.unit.dp
import androidx.compose.material.icons.outlined.PhonelinkLock
import androidx.compose.material3.Surface
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.window.Dialog
import androidx.compose.ui.window.DialogProperties
import com.hamzalabs.maktaba.core.R
import com.hamzalabs.maktaba.core.api.ApiClient
import com.hamzalabs.maktaba.core.api.User
import com.hamzalabs.maktaba.core.session.AppSession
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext

/** Posters already decoded, by home and video. */
private val posters = LruCache<String, ImageBitmap>(120)

/** A picture the home serves behind sign-in (a video's poster): loaded with the token. */
@Composable
fun Poster(client: ApiClient, video: String, modifier: Modifier = Modifier) {
    val key = "${client.home}|$video"
    var image by remember(key) { mutableStateOf(posters.get(key)) }
    LaunchedEffect(key) {
        if (image != null) return@LaunchedEffect
        val bytes = runCatching { client.poster(video) }.getOrNull() ?: return@LaunchedEffect
        val bmp = withContext(Dispatchers.Default) { BitmapFactory.decodeByteArray(bytes, 0, bytes.size)?.asImageBitmap() }
        if (bmp != null) {
            posters.put(key, bmp)
            image = bmp
        }
    }
    Box(
        modifier.aspectRatio(16f / 9f).background(MaterialTheme.colorScheme.surfaceVariant),
        contentAlignment = Alignment.Center,
    ) {
        val img = image
        if (img != null) {
            Image(img, contentDescription = null, contentScale = ContentScale.Crop, modifier = Modifier.fillMaxSize())
        } else {
            Icon(Icons.Outlined.Movie, null, tint = MaterialTheme.colorScheme.onSurfaceVariant)
        }
    }
}

/**
 * The account menu: who, which home, approving another device's sign-in (story 42.6), the
 * theme, switch home, sign out.
 */
@Composable
fun AccountMenu(session: AppSession, user: User, themes: ThemeStore) {
    var open by remember { mutableStateOf(false) }
    var approving by rememberSaveable { mutableStateOf(false) }
    val home by session.current.collectAsState()
    val mode by themes.mode.collectAsState()
    val client = session.client
    if (approving && client != null) {
        Dialog(
            onDismissRequest = { approving = false },
            properties = DialogProperties(usePlatformDefaultWidth = false, decorFitsSystemWindows = false),
        ) {
            Surface(Modifier.fillMaxSize()) {
                ApproveSignIn(
                    client = client,
                    username = user.username,
                    homeName = home?.label ?: client.home.host,
                    onClose = { approving = false },
                    onSignedOut = { approving = false; session.signedOut() },
                )
            }
        }
    }
    Box {
        IconButton(onClick = { open = true }) { Icon(Icons.Filled.AccountCircle, "Account") }
        DropdownMenu(expanded = open, onDismissRequest = { open = false }) {
            DropdownMenuItem(text = { Text(user.username, style = MaterialTheme.typography.titleSmall) }, onClick = {}, enabled = false)
            home?.let { DropdownMenuItem(text = { Text(it.label) }, onClick = {}, enabled = false) }
            HorizontalDivider()
            DropdownMenuItem(
                text = { Text(stringResource(R.string.account_approve)) },
                leadingIcon = { Icon(Icons.Outlined.PhonelinkLock, null) },
                onClick = { open = false; approving = true },
                modifier = Modifier.testTag("account-approve"),
            )
            HorizontalDivider()
            ThemeMode.entries.forEach { m ->
                DropdownMenuItem(
                    text = { Text("Theme: ${m.label}") },
                    leadingIcon = { if (m == mode) Icon(Icons.Filled.Check, null) else Box(Modifier.size(24.dp)) },
                    onClick = { themes.set(m); open = false },
                )
            }
            HorizontalDivider()
            DropdownMenuItem(text = { Text("Switch home") }, onClick = { open = false; session.switchHome() })
            DropdownMenuItem(text = { Text("Sign out") }, onClick = { open = false; session.signOut() })
        }
    }
}

/** An empty or failed state in a pane: a title and, when there is one, why. */
@Composable
fun Message(title: String, modifier: Modifier = Modifier, detail: String? = null) {
    Column(
        modifier.fillMaxSize().padding(24.dp),
        verticalArrangement = Arrangement.spacedBy(8.dp, Alignment.CenterVertically),
        horizontalAlignment = Alignment.CenterHorizontally,
    ) {
        Text(title, style = MaterialTheme.typography.titleMedium, textAlign = TextAlign.Center)
        if (detail != null) {
            Text(detail, style = MaterialTheme.typography.bodyMedium, color = MaterialTheme.colorScheme.onSurfaceVariant, textAlign = TextAlign.Center)
        }
    }
}

/** The status and navigation bars' icons, light on a dark theme and dark on a light one. */
@Composable
fun SystemBars(dark: Boolean) {
    val activity = LocalActivity.current as? ComponentActivity ?: return
    val scrim = MaterialTheme.colorScheme.background.copy(alpha = 0f).toArgb()
    SideEffect {
        val style = if (dark) SystemBarStyle.dark(scrim) else SystemBarStyle.light(scrim, scrim)
        activity.enableEdgeToEdge(statusBarStyle = style, navigationBarStyle = style)
    }
}

/** Seconds as the player shows them: 1:02:03, or 4:05 under an hour. */
fun formatDuration(sec: Double): String {
    val s = sec.toLong()
    val h = s / 3600
    val m = (s % 3600) / 60
    val r = s % 60
    return if (h > 0) "%d:%02d:%02d".format(h, m, r) else "%d:%02d".format(m, r)
}
