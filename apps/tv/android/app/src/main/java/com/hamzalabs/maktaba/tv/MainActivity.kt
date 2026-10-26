package com.hamzalabs.maktaba.tv

import android.os.Bundle
import androidx.activity.ComponentActivity
import androidx.activity.compose.setContent
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.ui.Modifier
import androidx.tv.material3.MaterialTheme
import androidx.tv.material3.Surface
import androidx.tv.material3.SurfaceDefaults
import com.hamzalabs.maktaba.tv.ui.Root
import com.hamzalabs.maktaba.tv.ui.theme.MaktabaTvTheme

/** The one activity: the theme and [Root], which follows the session (home, sign-in, the app). */
class MainActivity : ComponentActivity() {
    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        val app = application as MaktabaTVApp
        setContent {
            MaktabaTvTheme {
                Surface(
                    modifier = Modifier.fillMaxSize(),
                    colors = SurfaceDefaults.colors(containerColor = MaterialTheme.colorScheme.background),
                ) {
                    Root(app)
                }
            }
        }
    }
}
