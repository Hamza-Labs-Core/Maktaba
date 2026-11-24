// The Player's window: Maktaba's own title bar (Epic 13 story 13.15, designed in 13.9 and
// approved as D4; docs/design/desktop/mockups/player-*.html).
//
// On the JetBrains Runtime (the runtime the installers carry: build.gradle.kts, -Pmaktaba.jbr)
// the window is Jewel's DecoratedWindow: the system keeps the window's buttons, its resizing,
// its snapping and Windows 11's Snap Layouts, and the Player draws the rest of the title bar
// (the menu, the name, big screen and help) in its own colours. Anywhere else (a development
// run on another JDK, or MAKTABA_PLAIN_FRAME=1) the window is the system's, and the same bar is
// the first row of the page. Either way there is no Swing menu bar: its items are in the
// bar's menu (FrameMenu), and F11 still switches big screen.
//
// The title bar keeps the system's direction (its buttons are the system's, on the right of a
// Windows window); the menu it opens, like the page, follows the Player's language.
package com.hamzalabs.maktaba.desktop.shell

import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.layout.widthIn
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.outlined.HelpOutline
import androidx.compose.material.icons.outlined.Check
import androidx.compose.material.icons.outlined.Fullscreen
import androidx.compose.material.icons.outlined.FullscreenExit
import androidx.compose.material.icons.outlined.Menu
import androidx.compose.material3.DropdownMenu
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.MutableState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.input.key.Key
import androidx.compose.ui.input.key.KeyEvent
import androidx.compose.ui.input.key.KeyEventType
import androidx.compose.ui.input.key.isCtrlPressed
import androidx.compose.ui.input.key.isShiftPressed
import androidx.compose.ui.input.key.key
import androidx.compose.ui.input.key.type
import androidx.compose.ui.platform.LocalLayoutDirection
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.res.painterResource
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.DpSize
import androidx.compose.ui.unit.LayoutDirection
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.compose.ui.window.ApplicationScope
import androidx.compose.ui.window.Window
import androidx.compose.ui.window.WindowState
import com.hamzalabs.maktaba.desktop.Lang
import com.hamzalabs.maktaba.desktop.PlayerTheme
import com.hamzalabs.maktaba.desktop.Texts
import com.hamzalabs.maktaba.desktop.Tokens
import org.jetbrains.jewel.foundation.theme.JewelTheme
import org.jetbrains.jewel.intui.standalone.theme.IntUiTheme
import org.jetbrains.jewel.intui.standalone.theme.darkThemeDefinition
import org.jetbrains.jewel.intui.standalone.theme.default
import org.jetbrains.jewel.intui.window.decoratedWindow
import org.jetbrains.jewel.intui.window.styling.dark
import org.jetbrains.jewel.intui.window.styling.defaults
import org.jetbrains.jewel.ui.ComponentStyling
import org.jetbrains.jewel.window.DecoratedWindow
import org.jetbrains.jewel.window.TitleBar
import org.jetbrains.jewel.window.styling.DecoratedWindowColors
import org.jetbrains.jewel.window.styling.DecoratedWindowStyle
import org.jetbrains.jewel.window.styling.TitleBarColors
import org.jetbrains.jewel.window.styling.TitleBarMetrics
import org.jetbrains.jewel.window.styling.TitleBarStyle

/** The chrome's colours, from the token build (Tokens.kt: desktop.chrome, line, hover). */
object FrameColors {
    val Chrome = Tokens.Desktop.chrome
    val Line = Tokens.Desktop.line
    val Fg = Tokens.Ink.fg
    val Muted = Tokens.Ink.muted
    val Hover = Tokens.Desktop.hover
    val Menu = Tokens.Desktop.raised
    /** Windows' own close-button red: people read it as "close" */
    val CloseHover = Tokens.Desktop.closeHover
}

/** The title bar's height: the design's 44 px. */
val FRAME_HEIGHT = 44.dp

/** What the Swing menu bar held, for the bar's menu. A null action is not offered. */
class FrameMenu(
    val signedInLine: String? = null,
    val approve: (() -> Unit)? = null,
    /** "Send crash reports to <home>" and whether it is on */
    val crashReports: Pair<String, Boolean>? = null,
    val onCrashReports: (Boolean) -> Unit = {},
    val signOut: (() -> Unit)? = null,
    val switchHome: (() -> Unit)? = null,
    val big: Boolean = false,
    val onBig: (Boolean) -> Unit = {},
    val startBig: Boolean = false,
    val onStartBig: (Boolean) -> Unit = {},
    val lang: Lang = Lang.En,
    val onLang: (Lang) -> Unit = {},
    val help: () -> Unit = {},
    val updates: () -> Unit = {},
    val reportProblem: (() -> Unit)? = null,
    val myReports: (() -> Unit)? = null,
)

/** Whether this runtime can give the window Maktaba's title bar (the JetBrains Runtime's). */
fun decoratedFrameAvailable(env: Map<String, String> = System.getenv()): Boolean {
    if (env["MAKTABA_PLAIN_FRAME"] == "1") return false
    // the JetBrains Runtime's API (Jewel's dependency, reached by name: it is not this module's)
    return runCatching {
        Class.forName("com.jetbrains.JBR").getMethod("isAvailable").invoke(null) as Boolean
    }.getOrDefault(false)
}

/**
 * The Player's window: Maktaba's title bar on the JetBrains Runtime, the system's frame with
 * the same bar as the page's first row elsewhere. [content] is the page.
 */
@Composable
fun ApplicationScope.PlayerWindow(
    onCloseRequest: () -> Unit,
    state: WindowState,
    texts: Texts,
    menu: FrameMenu,
    onPreviewKeyEvent: (KeyEvent) -> Boolean,
    content: @Composable () -> Unit,
) {
    val title = texts["app.title"]
    val decorated = remember {
        decoratedFrameAvailable().also {
            // which frame this run has: CI's launch on the packaged runtime reads it
            System.err.println("maktaba-player: frame ${if (it) "decorated" else "plain"}")
        }
    }
    // F10 opens the menu, as it opened the menu bar on Windows and Linux
    val menuOpen = remember { mutableStateOf(false) }
    val keys: (KeyEvent) -> Boolean = { e ->
        if (e.type == KeyEventType.KeyDown && e.key == Key.F10 && !e.isShiftPressed && !e.isCtrlPressed) {
            menuOpen.value = true
            true
        } else {
            onPreviewKeyEvent(e)
        }
    }
    if (decorated) {
        IntUiTheme(
            theme = JewelTheme.darkThemeDefinition(),
            styling = ComponentStyling.default().decoratedWindow(
                windowStyle = DecoratedWindowStyle.dark(DecoratedWindowColors.dark(borderColor = FrameColors.Line)),
                titleBarStyle = TitleBarStyle.dark(
                    colors = TitleBarColors.dark(
                        backgroundColor = FrameColors.Chrome,
                        inactiveBackground = FrameColors.Chrome,
                        contentColor = FrameColors.Fg,
                        borderColor = FrameColors.Line,
                        titlePaneCloseButtonHoveredBackground = FrameColors.CloseHover,
                        iconButtonHoveredBackground = FrameColors.Hover,
                        iconButtonPressedBackground = FrameColors.Hover,
                    ),
                    metrics = TitleBarMetrics.defaults(
                        height = FRAME_HEIGHT,
                        titlePaneButtonSize = DpSize(46.dp, FRAME_HEIGHT),
                    ),
                ),
            ),
            swingCompatMode = false,
        ) {
            DecoratedWindow(
                onCloseRequest = onCloseRequest,
                state = state,
                title = title,
                onPreviewKeyEvent = keys,
            ) {
                // the title bar's layout stays left to right whatever the system's locale (the
                // system's buttons keep their side); the page sets its own direction
                CompositionLocalProvider(LocalLayoutDirection provides LayoutDirection.Ltr) {
                    TitleBar(Modifier.testTag("frame-title-bar")) {
                        // the bar's own row, at the start; the system's buttons keep the end
                        Box(Modifier.align(Alignment.Start)) {
                            PlayerTheme(texts, big = false) { FrameBar(texts, menu, drawBackground = false, menuOpen) }
                        }
                    }
                    content()
                }
            }
        }
    } else {
        Window(
            onCloseRequest = onCloseRequest,
            state = state,
            title = title,
            onPreviewKeyEvent = keys,
        ) {
            Column(Modifier.fillMaxSize()) {
                PlayerTheme(texts, big = false) { FrameBar(texts, menu, drawBackground = true, menuOpen) }
                Box(Modifier.weight(1f)) { content() }
            }
        }
    }
}

/**
 * The bar: the menu button, the app's icon and name, then big screen and help. In the title
 * bar it sits in the system's direction; as the page's first row it spans the window.
 */
@Composable
fun FrameBar(
    texts: Texts,
    menu: FrameMenu,
    drawBackground: Boolean,
    menuOpen: MutableState<Boolean> = remember { mutableStateOf(false) },
) {
    CompositionLocalProvider(LocalLayoutDirection provides LayoutDirection.Ltr) {
        Row(
            modifier = (if (drawBackground) Modifier.fillMaxWidth().background(FrameColors.Chrome) else Modifier)
                .height(FRAME_HEIGHT)
                .padding(horizontal = 6.dp)
                .testTag("frame-bar"),
            verticalAlignment = Alignment.CenterVertically,
        ) {
            FrameMenuButton(texts, menu, menuOpen)
            Spacer(Modifier.width(6.dp))
            Icon(
                painterResource("maktaba-player.png"),
                contentDescription = null,
                tint = Color.Unspecified,
                modifier = Modifier.size(18.dp),
            )
            Spacer(Modifier.width(10.dp))
            Text(texts["app.title"], color = FrameColors.Fg, fontSize = 13.sp, fontWeight = FontWeight.SemiBold)
            Spacer(Modifier.width(18.dp))
            val bigLabel = if (menu.big) texts["menu.leaveBigScreen"] else texts["menu.bigScreen"]
            IconButton(onClick = { menu.onBig(!menu.big) }, modifier = Modifier.testTag("frame-big-screen")) {
                Icon(
                    if (menu.big) Icons.Outlined.FullscreenExit else Icons.Outlined.Fullscreen,
                    contentDescription = bigLabel,
                    tint = FrameColors.Muted,
                )
            }
            IconButton(onClick = menu.help, modifier = Modifier.testTag("frame-help")) {
                Icon(Icons.AutoMirrored.Outlined.HelpOutline, contentDescription = texts["menu.help"], tint = FrameColors.Muted)
            }
        }
    }
}

/** The menu: everything the Swing menu bar held, in the Player's language and direction. */
@Composable
fun FrameMenuButton(texts: Texts, menu: FrameMenu, openState: MutableState<Boolean>) {
    var open by openState
    Box {
        IconButton(
            onClick = { open = true },
            modifier = Modifier.testTag("frame-menu").semantics { contentDescription = texts["frame.menu"] },
        ) {
            Icon(Icons.Outlined.Menu, contentDescription = null, tint = FrameColors.Fg)
        }
        CompositionLocalProvider(LocalLayoutDirection provides texts.lang.direction) {
            DropdownMenu(
                expanded = open,
                onDismissRequest = { open = false },
                containerColor = FrameColors.Menu,
                modifier = Modifier.widthIn(min = 300.dp),
            ) {
                FrameMenuItems(texts, menu) { open = false }
            }
        }
    }
}

/** The menu's items, grouped as the menu bar's were: account, view, language, help. */
@Composable
fun FrameMenuItems(texts: Texts, menu: FrameMenu, close: () -> Unit) {
    @Composable
    fun item(label: String, enabled: Boolean = true, checked: Boolean? = null, onClick: () -> Unit) {
        DropdownMenuItem(
            text = { Text(label, color = if (enabled) FrameColors.Fg else FrameColors.Muted) },
            enabled = enabled,
            modifier = if (enabled) Modifier.height(38.dp) else Modifier.heightIn(min = 38.dp),
            contentPadding = PaddingValues(horizontal = 14.dp),
            leadingIcon = {
                if (checked == true) Icon(Icons.Outlined.Check, null, tint = MaterialTheme.colorScheme.primary)
                else Spacer(Modifier.size(20.dp))
            },
            onClick = {
                close()
                onClick()
            },
        )
    }
    @Composable
    fun head(label: String) {
        Text(
            label,
            color = FrameColors.Muted,
            fontSize = 12.sp,
            fontWeight = FontWeight.SemiBold,
            modifier = Modifier.padding(start = 16.dp, end = 16.dp, top = 8.dp, bottom = 4.dp),
        )
    }
    head(texts["menu.account"])
    menu.signedInLine?.let { item(it, enabled = false) {} }
    menu.approve?.let { item(texts["menu.approve"], onClick = it) }
    menu.crashReports?.let { (label, on) -> item(label, checked = on) { menu.onCrashReports(!on) } }
    menu.signOut?.let { item(texts["menu.signOut"], onClick = it) }
    menu.switchHome?.let { item(texts["menu.switchHome"], onClick = it) }
    HorizontalDivider(color = FrameColors.Line, modifier = Modifier.padding(vertical = 4.dp))
    head(texts["menu.bigScreen"])
    item(if (menu.big) texts["menu.leaveBigScreen"] else texts["menu.bigScreen"]) { menu.onBig(!menu.big) }
    item(texts["menu.startBig"], checked = menu.startBig) { menu.onStartBig(!menu.startBig) }
    HorizontalDivider(color = FrameColors.Line, modifier = Modifier.padding(vertical = 4.dp))
    head(texts["menu.language"])
    for (l in Lang.entries) {
        item(if (l == Lang.Ar) "العربية" else "English", checked = menu.lang == l) { menu.onLang(l) }
    }
    HorizontalDivider(color = FrameColors.Line, modifier = Modifier.padding(vertical = 4.dp))
    head(texts["menu.help"])
    item(texts["menu.help"], onClick = menu.help)
    item(texts["menu.updates"], onClick = menu.updates)
    menu.reportProblem?.let { item(texts["menu.reportProblem"], onClick = it) }
    menu.myReports?.let { item(texts["menu.myReports"], onClick = it) }
}
