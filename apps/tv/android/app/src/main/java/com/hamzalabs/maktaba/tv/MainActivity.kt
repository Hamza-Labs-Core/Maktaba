package com.hamzalabs.maktaba.tv

import android.content.Intent
import android.os.Bundle
import androidx.activity.ComponentActivity
import androidx.activity.compose.setContent
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.ui.Modifier
import androidx.tv.material3.MaterialTheme
import androidx.tv.material3.Surface
import androidx.tv.material3.SurfaceDefaults
import com.hamzalabs.maktaba.tv.permissions.TvPermissionsGate
import com.hamzalabs.maktaba.tv.ui.Root
import com.hamzalabs.maktaba.tv.ui.theme.MaktabaTvTheme

/** The one activity: the theme and [Root], which follows the session (home, sign-in, the app). */
class MainActivity : ComponentActivity() {
    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        val app = application as MaktabaTVApp
        // a home's maktaba://connect link: offered, never followed before the person says so;
        // not again when the activity is recreated or reopened from Recents with the same intent
        val fromHistory = ((intent?.flags ?: 0) and Intent.FLAG_ACTIVITY_LAUNCHED_FROM_HISTORY) != 0
        if (savedInstanceState == null && !fromHistory) app.session.openLink(intent?.dataString)
        setContent {
            MaktabaTvTheme {
                Surface(
                    modifier = Modifier.fillMaxSize(),
                    colors = SurfaceDefaults.colors(containerColor = MaterialTheme.colorScheme.background),
                ) {
                    // first run, and whenever a required permission is missing (checked on resume)
                    TvPermissionsGate { Root(app) }
                }
            }
        }
    }

    override fun onNewIntent(intent: Intent) {
        super.onNewIntent(intent)
        (application as MaktabaTVApp).session.openLink(intent.dataString)
    }
}
