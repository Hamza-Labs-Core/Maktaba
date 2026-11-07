package com.hamzalabs.maktaba.player.plugins

import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import com.hamzalabs.maktaba.core.api.PanelView
import com.hamzalabs.maktaba.core.api.panels
import com.hamzalabs.maktaba.core.ui.PluginPanels
import com.hamzalabs.maktaba.player.shell.LocalPlayerHome
import com.hamzalabs.maktaba.player.shell.PlayerNavigator
import com.hamzalabs.maktaba.player.shell.Route
import com.hamzalabs.maktaba.player.shell.features
import kotlinx.coroutines.CancellationException

/**
 * A series' or library's plugin panels (story 54.20 §1): "From plugins", below the page's own
 * sections, only while the home answers `plugin_panels: on`. A chip searches Maktaba for its
 * text; a failure is no panels (they are extras, never the page).
 */
@Composable
fun SubjectPanels(kind: String, id: String, nav: PlayerNavigator, modifier: Modifier = Modifier, tv: Boolean = false) {
    val home = LocalPlayerHome.current
    val features = home.features()
    var panels by remember(kind, id) { mutableStateOf<List<PanelView>>(emptyList()) }
    LaunchedEffect(kind, id, features.panels) {
        panels = if (!features.panels) {
            emptyList()
        } else {
            try {
                home.client.panels(kind, id, if (java.util.Locale.getDefault().language == "ar") "ar" else "en")
            } catch (e: CancellationException) {
                throw e
            } catch (_: Exception) {
                emptyList()
            }
        }
    }
    PluginPanels(panels, modifier, tv = tv, onSearch = { nav.open(Route.SearchFor(it)) })
}
