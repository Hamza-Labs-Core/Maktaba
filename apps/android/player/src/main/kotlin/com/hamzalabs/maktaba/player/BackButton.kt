package com.hamzalabs.maktaba.player

import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.runtime.Composable
import androidx.compose.ui.res.stringResource
import com.hamzalabs.maktaba.core.R as CoreR

/** A back arrow for a pane's title bar (it mirrors in Arabic). */
@Composable
internal fun BackButton(onBack: () -> Unit) {
    IconButton(onClick = onBack) { Icon(Icons.AutoMirrored.Filled.ArrowBack, stringResource(CoreR.string.error_back)) }
}
