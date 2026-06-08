package com.hamzalabs.maktaba.tv

import android.os.Bundle
import androidx.activity.ComponentActivity
import androidx.activity.compose.setContent
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.ui.Modifier
import androidx.tv.material3.ExperimentalTvMaterial3Api
import androidx.tv.material3.MaterialTheme
import androidx.tv.material3.Surface
import com.hamzalabs.maktaba.tv.ui.navigation.NavGraph
import com.hamzalabs.maktaba.tv.ui.navigation.Routes
import com.hamzalabs.maktaba.tv.ui.theme.MaktabaTvTheme

/**
 * Single-activity host. Android TV apps are conventionally
 * single-activity + Compose navigation; the activity just installs the
 * theme and the nav graph. Unauthenticated users start on Settings
 * (the onboarding/sign-in surface).
 */
class MainActivity : ComponentActivity() {
    @OptIn(ExperimentalTvMaterial3Api::class)
    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)

        val container = (application as MaktabaTVApp).container
        val start = if (container.repository.isLoggedIn) Routes.HOME else Routes.SETTINGS

        setContent {
            MaktabaTvTheme {
                Surface(
                    modifier = Modifier.fillMaxSize(),
                    colors = androidx.tv.material3.SurfaceDefaults.colors(
                        containerColor = MaterialTheme.colorScheme.background,
                    ),
                ) {
                    NavGraph(
                        repository = container.repository,
                        settings = container.settings,
                        startDestination = start,
                    )
                }
            }
        }
    }
}
