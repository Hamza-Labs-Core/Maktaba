package com.hamzalabs.maktaba.tv.ui.screens

import androidx.compose.ui.platform.testTag
import android.app.LocaleManager
import android.os.Build
import android.os.LocaleList
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.runtime.Composable
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.unit.dp
import androidx.tv.material3.MaterialTheme
import androidx.tv.material3.Text
import com.hamzalabs.maktaba.core.api.User
import com.hamzalabs.maktaba.tv.MaktabaTVApp
import com.hamzalabs.maktaba.tv.R
import com.hamzalabs.maktaba.tv.ui.components.MaktabaButton
import com.hamzalabs.maktaba.tv.ui.components.ReadingPanel
import com.hamzalabs.maktaba.tv.ui.components.SectionTitle
import com.hamzalabs.maktaba.tv.ui.theme.MaktabaPalette
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import com.hamzalabs.maktaba.tv.ui.screens.library.PlaybackPrefs

/** Who is signed in where; switch home, sign out; the app's language (Android 13 and later). */
@Composable
fun AccountScreen(app: MaktabaTVApp, user: User, onPlaylists: () -> Unit = {}) {
    val home by app.session.current.collectAsState()
    val context = LocalContext.current
    Column(Modifier.fillMaxSize().padding(horizontal = Gutter, vertical = 32.dp)) {
        ReadingPanel(Modifier.width(720.dp), contentPadding = 32.dp) {
            SectionTitle(stringResource(R.string.tab_account))
            Text(stringResource(R.string.signed_in_as, user.username), style = MaterialTheme.typography.titleMedium, color = MaktabaPalette.Text, modifier = Modifier.padding(top = 16.dp))
            home?.let { Text(stringResource(R.string.home_label, it.name), style = MaterialTheme.typography.bodyLarge, color = MaktabaPalette.Muted) }
            Row(Modifier.padding(top = 24.dp), horizontalArrangement = Arrangement.spacedBy(16.dp)) {
                MaktabaButton(onClick = { app.session.switchHome() }) { Text(stringResource(R.string.switch_home)) }
                MaktabaButton(onClick = { app.session.signOut() }) { Text(stringResource(R.string.sign_out)) }
            }
            if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.TIRAMISU) {
                Text(stringResource(R.string.language), style = MaterialTheme.typography.titleLarge, color = MaktabaPalette.Text, modifier = Modifier.padding(top = 32.dp))
                Row(Modifier.padding(top = 12.dp), horizontalArrangement = Arrangement.spacedBy(16.dp)) {
                    val locales = context.getSystemService(LocaleManager::class.java)
                    MaktabaButton(onClick = { locales.applicationLocales = LocaleList.forLanguageTags("en") }) { Text("English") }
                    MaktabaButton(onClick = { locales.applicationLocales = LocaleList.forLanguageTags("ar") }) { Text("العربية") }
                }
            }
            // Epic 56: every playlist of the user, and the Playback setting (story 56.11)
            Row(Modifier.padding(top = 24.dp), horizontalArrangement = Arrangement.spacedBy(16.dp)) {
                MaktabaButton(onClick = onPlaylists) { Text(stringResource(R.string.playlists_title)) }
            }
            Text(stringResource(R.string.settings_playback), style = MaterialTheme.typography.titleLarge, color = MaktabaPalette.Text, modifier = Modifier.padding(top = 32.dp))
            var autoplay by remember { mutableStateOf(PlaybackPrefs.autoplay(context)) }
            MaktabaButton(
                onClick = { autoplay = !autoplay; PlaybackPrefs.setAutoplay(context, autoplay) },
                modifier = Modifier.padding(top = 12.dp),
            ) {
                Text(stringResource(R.string.settings_autoplay_next) + ": " + stringResource(if (autoplay) R.string.on else R.string.off))
            }
            // Epic 53: crash reports to the home (off until turned on), Report a problem, My reports
            home?.let { h ->
                val consent = remember { com.hamzalabs.maktaba.core.diagnostics.CrashConsent(context) }
                var crashes by remember(h) { mutableStateOf(consent.isOn(h)) }
                MaktabaButton(onClick = { crashes = !crashes; consent.set(crashes, h) }, modifier = Modifier.padding(top = 24.dp)) {
                    Text(stringResource(com.hamzalabs.maktaba.core.R.string.crash_setting, h.label) + ": " + stringResource(if (crashes) R.string.on else R.string.off))
                }
            }
            val report = com.hamzalabs.maktaba.core.ui.LocalReportProblem.current
            var mine by remember { mutableStateOf(false) }
            val client = (app.session.phase.collectAsState().value as? com.hamzalabs.maktaba.core.session.AppSession.Phase.SignedIn)?.client
            if (mine && client != null) com.hamzalabs.maktaba.core.ui.MyReportsDialog(client) { mine = false }
            Row(Modifier.padding(top = 12.dp), horizontalArrangement = Arrangement.spacedBy(16.dp)) {
                if (report != null) {
                    MaktabaButton(onClick = { report(null) }, modifier = Modifier.testTag("account-report")) {
                        Text(stringResource(com.hamzalabs.maktaba.core.R.string.report_title))
                    }
                }
                if (client != null) MaktabaButton(onClick = { mine = true }) { Text(stringResource(com.hamzalabs.maktaba.core.R.string.report_mine)) }
            }
            Text(stringResource(R.string.licences), style = MaterialTheme.typography.labelMedium, color = MaktabaPalette.Faint, modifier = Modifier.padding(top = 32.dp))
        }
    }
}
