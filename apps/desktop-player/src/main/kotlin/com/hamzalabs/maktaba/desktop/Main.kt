package com.hamzalabs.maktaba.desktop

import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Surface
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.focus.FocusDirection
import androidx.compose.ui.input.key.Key
import androidx.compose.ui.input.key.KeyEventType
import androidx.compose.ui.input.key.key
import androidx.compose.ui.input.key.onKeyEvent
import androidx.compose.ui.input.key.type
import androidx.compose.ui.platform.LocalFocusManager
import androidx.compose.ui.platform.LocalLayoutDirection
import androidx.compose.ui.unit.DpSize
import androidx.compose.ui.unit.LayoutDirection
import androidx.compose.ui.unit.dp
import androidx.compose.ui.window.MenuBar
import androidx.compose.ui.window.Window
import androidx.compose.ui.window.WindowPlacement
import androidx.compose.ui.window.application
import androidx.compose.ui.window.rememberWindowState
import com.hamzalabs.maktaba.core.api.ClientHeader
import com.hamzalabs.maktaba.core.session.AppSession
import com.hamzalabs.maktaba.core.session.FoundHome
import javafx.application.Platform
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.flow.emptyFlow
import java.awt.Desktop
import javax.jmdns.JmmDNS
import kotlin.system.exitProcess
import java.net.URI

/** This app as the home sees it (`X-Maktaba-Client`): a label, never a right. */
fun clientHeader(version: String = BuildInfo.VERSION, os: String = System.getProperty("os.name")): String {
    val platform = os.lowercase().let {
        when {
            it.startsWith("windows") -> "windows"
            it.startsWith("mac") -> "macos"
            else -> "linux"
        }
    }
    return "player/$version ($platform; desktop)"
}

private const val KEY_LANG = "lang"
private const val KEY_START_BIG = "start_big"

fun main(args: Array<String>) {
    // JavaFX lives as long as the player, not as long as a video
    Platform.setImplicitExit(false)
    ClientHeader.use(clientHeader())
    val stores = Stores()
    // the session's own work (restoring a sign-in) and what must outlive a screen (a stopped
    // video's last position)
    val background = CoroutineScope(SupervisorJob() + Dispatchers.IO)
    val session = AppSession(stores.settings, CoroutineScope(SupervisorJob() + Dispatchers.Main), stores::tokens)
    // a home's link handed to the player (`maktaba://connect?…`, by the system or a shortcut): asked, never followed
    args.firstOrNull { it.startsWith("maktaba:", ignoreCase = true) }?.let(session::openLink)

    application {
        var lang by remember { mutableStateOf(Lang.of(stores.settings.get(KEY_LANG))) }
        var startBig by remember { mutableStateOf(stores.settings.get(KEY_START_BIG) == "true") }
        var big by remember { mutableStateOf(startBig) }
        var approving by remember { mutableStateOf(false) }
        var updating by remember { mutableStateOf(false) }
        val texts = remember(lang) { Texts(lang) }
        val phase by session.phase.collectAsState()
        val home by session.current.collectAsState()
        val window = rememberWindowState(size = DpSize(1280.dp, 800.dp))
        window.placement = if (big) WindowPlacement.Fullscreen else WindowPlacement.Floating

        Window(
            onCloseRequest = ::exitApplication,
            state = window,
            title = texts["app.title"],
            onPreviewKeyEvent = { e ->
                if (e.type == KeyEventType.KeyDown && e.key == Key.F11) {
                    big = !big
                    true
                } else false
            },
        ) {
            MenuBar {
                Menu(texts["menu.account"]) {
                    val signedIn = phase as? AppSession.Phase.SignedIn
                    if (signedIn != null) {
                        Item(texts["menu.signedInAs", signedIn.user.username, home?.label ?: ""], enabled = false, onClick = {})
                        Item(texts["menu.approve"], onClick = { approving = true })
                        Item(texts["menu.signOut"], onClick = { session.signOut() })
                    }
                    if (phase !is AppSession.Phase.ChoosingHome) Item(texts["menu.switchHome"], onClick = { session.switchHome() })
                }
                Menu(texts["menu.bigScreen"]) {
                    Item(if (big) texts["menu.leaveBigScreen"] else texts["menu.bigScreen"], onClick = { big = !big })
                    CheckboxItem(texts["menu.startBig"], checked = startBig, onCheckedChange = {
                        startBig = it
                        stores.settings.put(KEY_START_BIG, it.toString())
                    })
                }
                Menu(texts["menu.language"]) {
                    for (l in Lang.entries) {
                        RadioButtonItem(if (l == Lang.Ar) "العربية" else "English", selected = lang == l, onClick = {
                            lang = l
                            stores.settings.put(KEY_LANG, l.code)
                        })
                    }
                }
                Menu(texts["menu.help"]) {
                    Item(texts["menu.help"], onClick = { openHelp(home?.url) })
                    Item(texts["menu.updates"], onClick = { updating = true })
                }
            }
            PlayerTheme(texts, big) {
                Surface(Modifier.fillMaxSize(), color = MaterialTheme.colorScheme.background) {
                    PlayerApp(session, background)
                }
                session.link.collectAsState().value?.let { ConnectLinkDialog(session, it) }
                val signedIn = phase as? AppSession.Phase.SignedIn
                if (approving && signedIn != null) ApproveDialog(signedIn.client, onDismiss = { approving = false })
                if (updating) UpdateDialog(onDismiss = { updating = false }, onInstalling = ::exitApplication)
            }
        }
    }
    // the window is closed: JavaFX's thread and the network browser's would keep the process alive
    runCatching { JmmDNS.Factory.close() }
    Platform.exit()
    exitProcess(0)
}

/** The help page for this player: the home's own (every edition serves it), else the project's. */
fun openHelp(home: String?) {
    val url = (home?.let { "$it/help/desktop-player" } ?: "https://github.com/Hamza-Labs-Core/Maktaba/blob/main/web/src/help/content/en/desktop-player.md")
    runCatching { if (Desktop.isDesktopSupported()) Desktop.getDesktop().browse(URI(url)) }
}

/** The screens: a home to choose, signing in, then the library and the video. */
@Composable
fun PlayerApp(session: AppSession, background: CoroutineScope) {
    val phase by session.phase.collectAsState()
    val focus = LocalFocusManager.current
    val rtl = LocalLayoutDirection.current == LayoutDirection.Rtl
    // the arrows move the focus between cards and buttons (big screen's D-pad); a text field
    // keeps them for its cursor (it uses them before they get here)
    val arrows = Modifier.onKeyEvent { e ->
        if (e.type != KeyEventType.KeyDown) return@onKeyEvent false
        val d = when (e.key) {
            Key.DirectionUp -> FocusDirection.Up
            Key.DirectionDown -> FocusDirection.Down
            Key.DirectionLeft -> if (rtl) FocusDirection.Right else FocusDirection.Left
            Key.DirectionRight -> if (rtl) FocusDirection.Left else FocusDirection.Right
            else -> return@onKeyEvent false
        }
        focus.moveFocus(d)
    }
    Surface(arrows.fillMaxSize(), color = MaterialTheme.colorScheme.background) {
        when (val p = phase) {
            AppSession.Phase.ChoosingHome -> {
                val found by remember { discoverOrNothing() }.collectAsState(emptyList())
                HomePicker(session, found)
            }
            AppSession.Phase.SigningIn -> SignIn(session)
            is AppSession.Phase.SignedIn -> SignedIn(p, session, background)
        }
    }
}

/** The homes on this network; none when this computer's networks cannot be browsed. */
private fun discoverOrNothing() = runCatching { discoverHomes() }.getOrElse { emptyFlow<List<FoundHome>>() }

@Composable
private fun SignedIn(p: AppSession.Phase.SignedIn, session: AppSession, background: CoroutineScope) {
    val scope = rememberCoroutineScope()
    val playback = remember(p.client) { Playback(p.client, scope, background, session::signedOut) }
    val posters = remember(p.client) { Posters(p.client) }
    DisposableEffect(playback) { onDispose { playback.close() } }
    if (playback.video != null) {
        Watch(playback, onClose = { playback.stop() })
    } else {
        LibraryBrowser(p.client, posters, onPlay = playback::play, onSignedOut = session::signedOut)
    }
}
