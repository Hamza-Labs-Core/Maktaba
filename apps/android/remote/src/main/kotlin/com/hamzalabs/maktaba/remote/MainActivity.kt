package com.hamzalabs.maktaba.remote

import android.content.Intent
import android.os.Bundle
import androidx.activity.ComponentActivity
import androidx.activity.compose.setContent
import androidx.activity.enableEdgeToEdge
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.padding
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Lock
import androidx.compose.material3.Button
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.unit.dp
import com.hamzalabs.maktaba.core.api.User
import com.hamzalabs.maktaba.core.session.AppSession
import com.hamzalabs.maktaba.core.permissions.PermissionsGate
import com.hamzalabs.maktaba.core.permissions.permissionsHelpUrl
import com.hamzalabs.maktaba.core.ui.MaktabaApp
import com.hamzalabs.maktaba.core.ui.MaktabaTheme
import com.hamzalabs.maktaba.core.ui.SessionGate
import com.hamzalabs.maktaba.core.R as CoreR

/**
 * Maktaba Remote: run a home from a phone, a foldable or a tablet (its admins only; the home
 * refuses anyone else, whatever this app shows). It opens on what needs attention.
 */
class MainActivity : ComponentActivity() {
    override fun onCreate(savedInstanceState: Bundle?) {
        enableEdgeToEdge()
        super.onCreate(savedInstanceState)
        val app = application as RemoteApplication
        // opened by a home's link (maktaba://connect…): the app asks before it connects
        if (savedInstanceState == null) app.session.openLink(intent?.dataString)
        setContent {
            val mode by app.themes.mode.collectAsState()
            MaktabaTheme(mode) {
                Surface {
                    // first run, and whenever a required permission is missing (checked on resume)
                    PermissionsGate(MaktabaApp.Remote, helpUrl = { app.session.current.value?.url?.let(::permissionsHelpUrl) }) {
                        SessionGate(app.session, MaktabaApp.Remote) { user, client ->
                            if (user.isAdmin) RemoteHome(app.session, user, client, app.themes) else NotAnAdmin(app.session, user)
                        }
                    }
                }
            }
        }
    }

    override fun onNewIntent(intent: Intent) {
        super.onNewIntent(intent)
        (application as RemoteApplication).session.openLink(intent.dataString)
    }
}

/** A home's user who is not its admin: this app is not for them. */
@Composable
fun NotAnAdmin(session: AppSession, user: User) {
    Column(
        Modifier.fillMaxSize().padding(32.dp),
        verticalArrangement = Arrangement.spacedBy(16.dp, Alignment.CenterVertically),
        horizontalAlignment = Alignment.CenterHorizontally,
    ) {
        Icon(Icons.Filled.Lock, null, tint = MaterialTheme.colorScheme.primary)
        Text(stringResource(R.string.not_admin_title), style = MaterialTheme.typography.titleLarge)
        Text(
            stringResource(R.string.not_admin_body, user.username),
            textAlign = TextAlign.Center,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
        )
        Button(onClick = session::signOut) { Text(stringResource(CoreR.string.sign_out)) }
    }
}
