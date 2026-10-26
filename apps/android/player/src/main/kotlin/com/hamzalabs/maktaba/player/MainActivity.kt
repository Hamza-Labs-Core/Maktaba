package com.hamzalabs.maktaba.player

import android.os.Bundle
import androidx.activity.ComponentActivity
import androidx.activity.compose.setContent
import androidx.activity.enableEdgeToEdge
import androidx.compose.material3.Surface
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import com.hamzalabs.maktaba.core.permissions.PermissionsGate
import com.hamzalabs.maktaba.core.permissions.permissionsHelpUrl
import com.hamzalabs.maktaba.core.ui.MaktabaApp
import com.hamzalabs.maktaba.core.ui.MaktabaTheme
import com.hamzalabs.maktaba.core.ui.SessionGate

/** Maktaba Player: browse a home's libraries and watch, on a phone, a foldable or a tablet. */
class MainActivity : ComponentActivity() {
    override fun onCreate(savedInstanceState: Bundle?) {
        enableEdgeToEdge()
        super.onCreate(savedInstanceState)
        val app = application as PlayerApplication
        setContent {
            val mode by app.themes.mode.collectAsState()
            var playing by remember { mutableStateOf(false) }
            // dark while a video plays, as on the web and the Apple apps
            MaktabaTheme(mode, forceDark = playing) {
                Surface {
                    // first run, and whenever a required permission is missing (checked on resume)
                    PermissionsGate(MaktabaApp.Player, helpUrl = { app.session.current.value?.url?.let(::permissionsHelpUrl) }) {
                        SessionGate(app.session, MaktabaApp.Player) { user, client ->
                            LibraryBrowser(app.session, user, client, app.themes, onPlaying = { playing = it })
                        }
                    }
                }
            }
        }
    }
}
