package com.hamzalabs.maktaba.desktop

import kotlinx.coroutines.launch
import com.hamzalabs.maktaba.desktop.diagnostics.CrashConsent
import com.hamzalabs.maktaba.desktop.diagnostics.CrashQuestionDialog
import com.hamzalabs.maktaba.desktop.diagnostics.DesktopCrashes
import com.hamzalabs.maktaba.desktop.diagnostics.MyReportsDialog
import com.hamzalabs.maktaba.desktop.diagnostics.ReportProblemDialog
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
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
import androidx.compose.ui.window.Window
import androidx.compose.ui.window.WindowPlacement
import androidx.compose.ui.window.application
import com.hamzalabs.maktaba.desktop.shell.FrameMenu
import com.hamzalabs.maktaba.desktop.shell.MaktabaAlertDialog
import com.hamzalabs.maktaba.desktop.shell.PlayerWindow
import androidx.compose.ui.window.rememberWindowState
import com.hamzalabs.maktaba.core.api.ClientHeader
import com.hamzalabs.maktaba.core.api.capabilitiesGate
import com.hamzalabs.maktaba.core.session.KeyValueStore
import com.hamzalabs.maktaba.core.session.TokenStore
import com.hamzalabs.maktaba.desktop.shell.AppPrefs
import com.hamzalabs.maktaba.desktop.shell.DesktopHome
import com.hamzalabs.maktaba.desktop.shell.HomeActions
import com.hamzalabs.maktaba.desktop.shell.LocalDesktopHome
import com.hamzalabs.maktaba.desktop.shell.Shell
import com.hamzalabs.maktaba.desktop.shell.features
import com.hamzalabs.maktaba.desktop.shell.openInBrowser
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.LaunchedEffect
import com.hamzalabs.maktaba.core.session.AppSession
import com.hamzalabs.maktaba.core.session.FoundHome
import javafx.application.Platform
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.flow.emptyFlow
import javax.jmdns.JmmDNS
import kotlin.system.exitProcess

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

/**
 * Story 51.11's gate for this build (issue #638): the Gradle property `maktaba.capabilitiesGate`
 * (`-Pmaktaba.capabilitiesGate=off`), written into [BuildInfo.CAPABILITIES_GATE]. "off", "no",
 * "false" or "0" turn it off; anything else, or nothing, leaves it on. Release builds pass
 * nothing: the gate is on.
 */
fun buildCapabilitiesGate(setting: String = BuildInfo.CAPABILITIES_GATE): Boolean = capabilitiesGate(setting)

/** The Player's session, with this build's capabilities gate. */
fun playerSession(settings: KeyValueStore, scope: CoroutineScope, tokens: (String) -> TokenStore): AppSession =
    AppSession(settings, scope, tokens, capabilitiesGate = buildCapabilitiesGate())

fun main(args: Array<String>) {
    // JavaFX lives as long as the player, not as long as a video
    Platform.setImplicitExit(false)
    ClientHeader.use(clientHeader())
    val stores = Stores()
    // an uncaught exception is kept (class and frames, no message) for the home, sent only
    // when crash reports are on (Epic 53 story 53.14)
    DesktopCrashes.install(AppDirs.config, BuildInfo.VERSION)
    val crashOutbox = DesktopCrashes.outbox(AppDirs.config)
    val crashConsent = CrashConsent(stores.settings)
    val crashHeader = DesktopCrashes.header(DesktopCrashes.installId(stores.settings), BuildInfo.VERSION)
    // the session's own work (restoring a sign-in) and what must outlive a screen (a stopped
    // video's last position)
    val background = CoroutineScope(SupervisorJob() + Dispatchers.IO)
    val session = playerSession(stores.settings, CoroutineScope(SupervisorJob() + Dispatchers.Main), stores::tokens)
    val prefs = AppPrefs(stores.settings)
    // story 13.21: the video engine chosen in Profile, read at each video's open
    com.hamzalabs.maktaba.desktop.mpv.Engine.setting = { prefs.engine }
    // a home's link handed to the player (`maktaba://connect?…`, by the system or a shortcut): asked, never followed
    args.firstOrNull { it.startsWith("maktaba:", ignoreCase = true) }?.let(session::openLink)
    // the homes on this network, the whole time the player runs: a saved home is found again
    // where it announces itself
    session.discovery = ::discoverOrNothing
    session.discover(true)
    // story 13.19: what this machine decodes, and its audio device's channels, for device_caps
    com.hamzalabs.maktaba.desktop.mpv.Engine.startProbe()

    application {
        val lang = prefs.lang
        var startBig by remember { mutableStateOf(stores.settings.get(AppPrefs.KEY_START_BIG) == "true") }
        var big by remember { mutableStateOf(startBig) }
        var approving by remember { mutableStateOf(false) }
        var updating by remember { mutableStateOf(false) }
        var reporting by remember { mutableStateOf(false) }
        var reportRef by remember { mutableStateOf<String?>(null) }
        var crashNotice by remember { mutableStateOf<com.hamzalabs.maktaba.core.crash.CrashOutbox.Notice?>(null) }
        // story 13.18: a packaged Player whose libmpv did not load says so, and plays with JavaFX;
        // story 13.21: once per Player version and reason, not at every start
        val notices = remember { com.hamzalabs.maktaba.desktop.mpv.EngineNotice(stores.settings, BuildInfo.VERSION) }
        var engineNotice by remember { mutableStateOf(notices.pending(com.hamzalabs.maktaba.desktop.mpv.Engine.noticeReason())) }
        var myReports by remember { mutableStateOf(false) }
        var askCrashes by remember { mutableStateOf(false) }
        var crashesOn by remember { mutableStateOf(false) }
        val texts = remember(lang) { Texts(lang) }
        val phase by session.phase.collectAsState()
        val home by session.current.collectAsState()
        val window = rememberWindowState(size = DpSize(1280.dp, 800.dp))
        window.placement = if (big) WindowPlacement.Fullscreen else WindowPlacement.Floating

        val signedInPhase = phase as? AppSession.Phase.SignedIn
        // story 13.15: the Swing menu bar's items, in the title bar's menu (shell/PlayerFrame.kt)
        val frameMenu = FrameMenu(
            signedInLine = signedInPhase?.let { texts["menu.signedInAs", it.user.username, home?.label ?: ""] },
            approve = signedInPhase?.let { { approving = true } },
            crashReports = if (signedInPhase != null) home?.let { texts["menu.crashReports", it.label] to crashesOn } else null,
            onCrashReports = { on ->
                home?.let { h ->
                    crashesOn = on
                    crashConsent.set(on, h)
                }
            },
            signOut = signedInPhase?.let { { session.signOut() } },
            switchHome = if (phase !is AppSession.Phase.ChoosingHome) ({ session.switchHome() }) else null,
            big = big,
            onBig = { big = it },
            startBig = startBig,
            onStartBig = {
                startBig = it
                stores.settings.put(AppPrefs.KEY_START_BIG, it.toString())
            },
            lang = lang,
            onLang = { prefs.chooseLang(it) },
            help = { openHelp(home?.url) },
            updates = { updating = true },
            reportProblem = signedInPhase?.let { { reporting = true } },
            myReports = signedInPhase?.let { { myReports = true } },
        )

        PlayerWindow(
            onCloseRequest = ::exitApplication,
            state = window,
            texts = texts,
            menu = frameMenu,
            onPreviewKeyEvent = { e ->
                if (e.type == KeyEventType.KeyDown && e.key == Key.F11) {
                    big = !big
                    true
                } else false
            },
        ) {
            PlayerTheme(texts, big) {
                Surface(Modifier.fillMaxSize(), color = MaterialTheme.colorScheme.background) {
                    // "Report this" on every error box opens the form with that call's reference
                    androidx.compose.runtime.CompositionLocalProvider(
                        com.hamzalabs.maktaba.desktop.diagnostics.LocalReportProblem provides
                            (if (phase is AppSession.Phase.SignedIn) { ref: String? -> reportRef = ref; reporting = true } else null),
                    ) {
                    PlayerApp(
                        session, background, prefs,
                        HomeActions(
                            switchHome = session::switchHome,
                            signOut = session::signOut,
                            approve = { approving = true },
                            updates = { updating = true },
                            openUrl = ::openInBrowser,
                        ),
                        fullScreen = big,
                        onFullScreen = { big = it },
                    )
                    }
                }
                session.link.collectAsState().value?.let { ConnectLinkDialog(session, it) }
                val signedIn = phase as? AppSession.Phase.SignedIn
                if (approving && signedIn != null) ApproveDialog(signedIn.client, onDismiss = { approving = false })
                if (updating) UpdateDialog(onDismiss = { updating = false }, onInstalling = ::exitApplication)
                // Epic 53: crash reports to this home (asked once, off until yes) and Report a problem
                val signedHome = home
                if (signedIn != null && signedHome != null) {
                    LaunchedEffect(signedIn.client, signedHome) {
                        crashesOn = crashConsent.isOn(signedHome)
                        if (crashConsent.answer(signedHome) == null) askCrashes = true
                        else {
                            runCatching { DesktopCrashes.flush(crashOutbox, signedIn.client, signedHome, crashConsent, crashHeader) }
                            // what happened to the last crash, once (53.14 §4)
                            crashNotice = crashOutbox.notice()
                        }
                    }
                    if (askCrashes) {
                        val scope = rememberCoroutineScope()
                        CrashQuestionDialog(signedHome.label) { yes ->
                            crashConsent.set(yes, signedHome)
                            crashesOn = yes
                            askCrashes = false
                            scope.launch { runCatching { DesktopCrashes.flush(crashOutbox, signedIn.client, signedHome, crashConsent, crashHeader) } }
                        }
                    }
                    if (reporting) {
                        ReportProblemDialog(signedIn.client, signedHome.label, crashHeader, crashOutbox, reference = reportRef,
                            log = { com.hamzalabs.maktaba.desktop.mpv.Engine.logLines() },
                            onDismiss = { reporting = false; reportRef = null })
                    }
                    crashNotice?.let { n ->
                        MaktabaAlertDialog(
                            onDismissRequest = { crashNotice = null },
                            text = {
                                Text(if (n == com.hamzalabs.maktaba.core.crash.CrashOutbox.Notice.Sent) texts["crash.lastCrash.sent", signedHome.label]
                                else texts["crash.lastCrash.kept"])
                            },
                            confirmButton = { androidx.compose.material3.TextButton(onClick = { crashNotice = null }) { Text(texts["report.done"]) } },
                        )
                    }
                    engineNotice?.let { why ->
                        // shown: not again for this version and reason
                        LaunchedEffect(why) { notices.said(why) }
                        MaktabaAlertDialog(
                            onDismissRequest = { engineNotice = null },
                            text = { Text(texts["engine.fallback", why]) },
                            dismissButton = {
                                androidx.compose.material3.TextButton(onClick = { openInBrowser(helpUrl(signedHome.url, lang, ENGINE_HELP)) }) {
                                    Text(texts["engine.learnMore"])
                                }
                            },
                            confirmButton = { androidx.compose.material3.TextButton(onClick = { engineNotice = null }) { Text(texts["report.done"]) } },
                        )
                    }
                    if (myReports) MyReportsDialog(signedIn.client, onDismiss = { myReports = false })
                }
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
    openInBrowser(home?.let { "${it.trimEnd('/')}/help/desktop-player" } ?: "https://github.com/Hamza-Labs-Core/Maktaba/blob/main/web/src/help/content/en/desktop-player.md")
}

/** The help page's section on the video engine (story 13.21). */
const val ENGINE_HELP = "video-engine"

/**
 * The home's help page for this Player in [lang] (`?lang=`, which the help page follows), at
 * [anchor]; with no home signed in, maktaba.dev's Player page in that language.
 */
fun helpUrl(home: String?, lang: Lang, anchor: String? = null): String =
    if (home.isNullOrBlank()) "https://maktaba.dev/" + (if (lang == Lang.Ar) "ar/" else "") + "apps/player/"
    else "${home.trimEnd('/')}/help/desktop-player?lang=${lang.code}" + (anchor?.let { "#$it" } ?: "")

/** The screens: a home to choose, signing in, then the library and the video. */
@Composable
fun PlayerApp(
    session: AppSession,
    background: CoroutineScope,
    prefs: AppPrefs,
    actions: HomeActions = HomeActions(),
    fullScreen: Boolean = false,
    onFullScreen: (Boolean) -> Unit = {},
) {
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
                val found by session.nearby.collectAsState()
                HomePicker(session, found)
            }
            AppSession.Phase.SigningIn -> SignIn(session)
            is AppSession.Phase.SignedIn -> SignedIn(p, session, background, prefs, actions, fullScreen, onFullScreen)
        }
    }
}

/** The homes on this network; none when this computer's networks cannot be browsed. */
private fun discoverOrNothing() = runCatching { discoverHomes() }.getOrElse { emptyFlow<List<FoundHome>>() }

@Composable
private fun SignedIn(
    p: AppSession.Phase.SignedIn,
    session: AppSession,
    background: CoroutineScope,
    prefs: AppPrefs,
    actions: HomeActions,
    fullScreen: Boolean,
    onFullScreen: (Boolean) -> Unit,
) {
    val scope = rememberCoroutineScope()
    val label = session.current.collectAsState().value?.label.orEmpty()
    val playback = remember(p.client) {
        Playback(p.client, scope, background, session::signedOut, settings = { prefs.playback.current }, features = { session.capabilities.value })
    }
    // a new sign-in or another home: a new DesktopHome, and every page's kept state with it
    val home = remember(p.client, p.user) {
        DesktopHome(
            p.user, p.client, label, prefs, background,
            capabilities = session.capabilities,
            actions = actions,
            onPlay = playback::play,
            onSignedOut = session::signedOut,
            // Epic 57 story 57.05: the limit as last known, so a change made while the Player was closed is seen
            limitKept = com.hamzalabs.maktaba.core.parental.KeptLimit.InFile.of(AppDirs.config, p.client.home, p.user.id),
        )
    }
    // what the home has on: asked at sign-in, then every five minutes while the window is open
    // (a desktop window has no background state); a failed answer keeps the last good one
    LaunchedEffect(p.client) { session.followCapabilities() }
    // the sign-in's parental limit (Epic 57), read at sign-in and each time a video is opened or closed
    LaunchedEffect(home, playback.request == null) { home.parental.refresh() }
    DisposableEffect(playback) { onDispose { playback.close() } }
    CompositionLocalProvider(LocalDesktopHome provides home) {
        if (playback.request != null) {
            Watch(
                playback, home.features(), home.webUrl, fullScreen, onFullScreen,
                onClose = {
                    playback.stop()
                    if (fullScreen) onFullScreen(false)
                },
                posters = home.posters,
                parental = home.parental,
            )
        } else {
            Shell(home)
        }
    }
}
