package com.hamzalabs.maktaba.tv.ui.screens

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

/** Who is signed in where; switch home, sign out; the app's language (Android 13 and later). */
@Composable
fun AccountScreen(app: MaktabaTVApp, user: User) {
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
            Text(stringResource(R.string.licences), style = MaterialTheme.typography.labelMedium, color = MaktabaPalette.Faint, modifier = Modifier.padding(top = 32.dp))
        }
    }
}
