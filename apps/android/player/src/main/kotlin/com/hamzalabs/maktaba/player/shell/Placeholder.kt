package com.hamzalabs.maktaba.player.shell

import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.Box
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Text
import androidx.compose.material3.TopAppBar
import androidx.compose.runtime.Composable
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.res.stringResource
import com.hamzalabs.maktaba.core.ui.Message
import com.hamzalabs.maktaba.player.BackButton
import com.hamzalabs.maktaba.player.R

/**
 * A screen whose story has not merged yet: its title and "Not available yet". Its section is
 * hidden ([PlayerSection.enabled]), so a person never reaches one; the shell's tests do.
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun PlaceholderScreen(title: String, nav: PlayerNavigator) {
    Scaffold(
        modifier = Modifier.testTag("placeholder"),
        topBar = {
            TopAppBar(
                title = { Text(title) },
                navigationIcon = { if (nav.canGoBack) BackButton { nav.back() } },
            )
        },
    ) { padding ->
        Box(Modifier.padding(padding)) { Message(stringResource(R.string.shell_not_ready)) }
    }
}
