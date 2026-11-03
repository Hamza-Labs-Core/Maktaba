package com.hamzalabs.maktaba.core.ui

import android.Manifest
import android.annotation.SuppressLint
import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.content.Context
import android.content.Intent
import android.content.pm.PackageManager
import android.os.Build
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.WindowInsets
import androidx.compose.foundation.layout.consumeWindowInsets
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.statusBars
import androidx.compose.foundation.layout.statusBarsPadding
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.outlined.ConnectedTv
import androidx.compose.material3.Button
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.semantics.LiveRegionMode
import androidx.compose.ui.semantics.liveRegion
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.unit.dp
import androidx.core.app.NotificationCompat
import androidx.core.app.NotificationManagerCompat
import androidx.core.content.ContextCompat
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.compose.LocalLifecycleOwner
import androidx.lifecycle.repeatOnLifecycle
import com.hamzalabs.maktaba.core.R
import com.hamzalabs.maktaba.core.api.ApiClient
import com.hamzalabs.maktaba.core.api.ApiException
import com.hamzalabs.maktaba.core.api.PendingSignIn
import com.hamzalabs.maktaba.core.api.User
import com.hamzalabs.maktaba.core.session.AppSession
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow

/**
 * The devices waiting for an approval, as the banner shows them: the newest not dismissed.
 * Dismissing hides every request listed now; a request that arrives later shows again.
 */
class PendingSignInsState {
    var requests by mutableStateOf<List<PendingSignIn>>(emptyList())
        private set
    private var dismissed by mutableStateOf<Set<String>>(emptySet())
    private val seen = mutableSetOf<String>()

    /** What the banner shows: the newest request not dismissed; null hides it. */
    val banner: PendingSignIn? get() = requests.firstOrNull { it.id !in dismissed }

    /** The home's list now (newest first); returns the requests never seen before. */
    fun update(list: List<PendingSignIn>): List<PendingSignIn> {
        requests = list
        // forget what is gone, so the sets stay small
        dismissed = dismissed.intersect(list.map { it.id }.toSet())
        val arrived = list.filter { it.id !in seen }
        seen += arrived.map { it.id }
        return arrived
    }

    fun dismiss() {
        dismissed = dismissed + requests.map { it.id }
    }
}

/**
 * "Open Approve a TV or device": a tapped sign-in notification asks for it (the activity
 * hands its intent to [handle]); the screen showing [PendingSignInsHost] opens it.
 */
object ApproveRequests {
    /** The notification's intent extra. */
    const val EXTRA = "com.hamzalabs.maktaba.APPROVE_SIGN_IN"

    private val _open = MutableStateFlow(false)
    val open: StateFlow<Boolean> = _open.asStateFlow()

    /** An activity's intent: opens Approve when a sign-in notification sent it. */
    fun handle(intent: Intent?) {
        if (intent?.getBooleanExtra(EXTRA, false) == true) _open.value = true
    }

    fun opened() {
        _open.value = false
    }
}

/** The system notification for a device waiting to sign in (the "Sign-in requests" channel). */
object SignInNotifications {
    const val CHANNEL = "sign_in_requests"
    private const val ID = 4206

    /**
     * Posts that [request] is waiting, when the person allows this app's notifications (never
     * asks for the permission: the banner says it in the app anyway). Tapping it opens the app
     * on Approve. False when it was not posted.
     */
    // the permission is the Remote's and the Player's to declare (their manifests, never the
    // TV's), and is checked here before posting
    @SuppressLint("MissingPermission")
    fun post(context: Context, request: PendingSignIn): Boolean {
        if (Build.VERSION.SDK_INT >= 33 &&
            ContextCompat.checkSelfPermission(context, Manifest.permission.POST_NOTIFICATIONS) != PackageManager.PERMISSION_GRANTED
        ) return false
        val manager = NotificationManagerCompat.from(context)
        if (!manager.areNotificationsEnabled()) return false
        if (Build.VERSION.SDK_INT >= 26) {
            val channel = NotificationChannel(CHANNEL, context.getString(R.string.pending_channel), NotificationManager.IMPORTANCE_HIGH)
            channel.description = context.getString(R.string.pending_channel_description)
            context.getSystemService(NotificationManager::class.java)?.createNotificationChannel(channel)
        }
        val open = (context.packageManager.getLaunchIntentForPackage(context.packageName) ?: return false)
            .addFlags(Intent.FLAG_ACTIVITY_SINGLE_TOP or Intent.FLAG_ACTIVITY_CLEAR_TOP)
            .putExtra(ApproveRequests.EXTRA, true)
        val tap = PendingIntent.getActivity(context, ID, open, PendingIntent.FLAG_UPDATE_CURRENT or PendingIntent.FLAG_IMMUTABLE)
        val title = context.getString(if (request.isTv) R.string.pending_notification_tv else R.string.pending_notification_device)
        val name = shownName(request, appNames(context::getString))
        val notification = NotificationCompat.Builder(context, CHANNEL)
            .setSmallIcon(R.drawable.ic_notification_tv)
            .setContentTitle(if (name != null) "$title: $name" else title)
            .setContentText(context.getString(R.string.pending_notification_text))
            .setCategory(NotificationCompat.CATEGORY_STATUS)
            .setPriority(NotificationCompat.PRIORITY_HIGH)
            .setContentIntent(tap)
            .setAutoCancel(true)
            .build()
        return try {
            manager.notify(ID, notification)
            true
        } catch (e: SecurityException) {
            false
        }
    }

    /** Takes the notification down (nothing waits any more). */
    fun clear(context: Context) = NotificationManagerCompat.from(context).cancel(ID)
}

/** The apps' names by X-Maktaba-Client app. */
internal fun appNames(string: (Int) -> String): Map<String, String> = mapOf(
    "tv" to string(R.string.pending_app_tv),
    "player" to string(R.string.pending_app_player),
    "remote" to string(R.string.pending_app_remote),
)

/** What to call a waiting device: its label, else its app by name, else null. */
fun shownName(r: PendingSignIn, apps: Map<String, String>): String? =
    r.deviceLabel.trim().ifBlank { apps[r.clientApp] ?: r.clientApp.trim() }.ifBlank { null }

/** How often the apps ask while open: about the Remote's own refresh. */
const val PENDING_POLL_MS = 12_000L

/**
 * [content] with, at its top, a banner while a TV or another device waits for an approval
 * on this home ("A TV is asking to sign in: … Enter code"), and a system notification for each
 * new one. Asks the home every [intervalMs] while the app is in front ([enabled]), never in
 * the background; an older home without the list is not asked again. "Enter code" (or the
 * notification) opens Approve a TV or device; the home still checks every approval.
 */
@Composable
fun PendingSignInsHost(
    session: AppSession,
    user: User,
    client: ApiClient,
    enabled: Boolean = true,
    intervalMs: Long = PENDING_POLL_MS,
    content: @Composable () -> Unit,
) {
    val state = remember(client) { PendingSignInsState() }
    val context = LocalContext.current.applicationContext
    val lifecycle = LocalLifecycleOwner.current.lifecycle
    var approving by rememberSaveable { mutableStateOf(false) }
    val asked by ApproveRequests.open.collectAsState()
    LaunchedEffect(asked) {
        if (asked) {
            approving = true
            ApproveRequests.opened()
        }
    }
    LaunchedEffect(client, enabled) {
        if (!enabled) return@LaunchedEffect
        lifecycle.repeatOnLifecycle(Lifecycle.State.STARTED) {
            while (true) {
                val list = try {
                    client.pendingSignIns()
                } catch (e: CancellationException) {
                    throw e
                } catch (e: ApiException) {
                    // signed out, unreachable or busy: the screens say so; ask again later
                    delay(intervalMs)
                    continue
                } ?: break // an older home: nothing to ask
                val arrived = state.update(list.requests)
                arrived.firstOrNull()?.let { SignInNotifications.post(context, it) }
                if (list.requests.isEmpty()) SignInNotifications.clear(context)
                delay(intervalMs)
            }
        }
    }
    ApproveSignInDialog(session, user, approving, client) { approving = false }
    val shown = if (enabled) state.banner else null
    Column(Modifier.fillMaxSize()) {
        if (shown != null) {
            PendingSignInBanner(
                shown,
                onEnterCode = { approving = true },
                onDismiss = state::dismiss,
                modifier = Modifier.statusBarsPadding(),
            )
        }
        // one place for the screens whether the banner shows or not: their state (the place,
        // the scroll, what is open) stays when it comes and goes. With the banner, it took the
        // status bar's room: the screen below does not add it again.
        Box(Modifier.weight(1f).then(if (shown != null) Modifier.consumeWindowInsets(WindowInsets.statusBars) else Modifier)) { content() }
    }
}

/** The banner: what is asking, "Enter code" and "Dismiss". */
@Composable
fun PendingSignInBanner(request: PendingSignIn, onEnterCode: () -> Unit, onDismiss: () -> Unit, modifier: Modifier = Modifier) {
    val apps = mapOf(
        "tv" to stringResource(R.string.pending_app_tv),
        "player" to stringResource(R.string.pending_app_player),
        "remote" to stringResource(R.string.pending_app_remote),
    )
    val name = shownName(request, apps)
    val text = when {
        request.isTv && name != null -> stringResource(R.string.pending_tv_named, name)
        request.isTv -> stringResource(R.string.pending_tv)
        name != null -> stringResource(R.string.pending_device_named, name)
        else -> stringResource(R.string.pending_device)
    }
    Surface(color = MaterialTheme.colorScheme.secondaryContainer, modifier = modifier.fillMaxWidth().testTag("pending-banner")) {
        Column(Modifier.padding(start = 16.dp, end = 8.dp, top = 12.dp, bottom = 4.dp)) {
            Row(horizontalArrangement = Arrangement.spacedBy(12.dp), verticalAlignment = Alignment.Top, modifier = Modifier.padding(end = 8.dp)) {
                Icon(Icons.Outlined.ConnectedTv, null, tint = MaterialTheme.colorScheme.onSecondaryContainer)
                Text(
                    text,
                    style = MaterialTheme.typography.bodyMedium,
                    color = MaterialTheme.colorScheme.onSecondaryContainer,
                    modifier = Modifier.weight(1f).semantics { liveRegion = LiveRegionMode.Polite }.testTag("pending-text"),
                )
            }
            Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.spacedBy(8.dp, Alignment.End), verticalAlignment = Alignment.CenterVertically) {
                TextButton(onClick = onDismiss, modifier = Modifier.testTag("pending-dismiss")) { Text(stringResource(R.string.pending_dismiss)) }
                Button(onClick = onEnterCode, modifier = Modifier.testTag("pending-enter")) { Text(stringResource(R.string.pending_enter_code)) }
            }
        }
    }
}
