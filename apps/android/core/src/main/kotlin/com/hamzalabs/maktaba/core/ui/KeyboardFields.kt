package com.hamzalabs.maktaba.core.ui

import androidx.compose.foundation.layout.WindowInsets
import androidx.compose.foundation.layout.ime
import androidx.compose.foundation.relocation.BringIntoViewRequester
import androidx.compose.foundation.relocation.bringIntoViewRequester
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.focus.onFocusChanged
import androidx.compose.ui.platform.LocalDensity

/**
 * A start screen's text field kept above the keyboard: brought into view when it takes focus,
 * and again each time the keyboard's height changes while it has focus (the keyboard slides
 * up after the focus moved, shrinking `StartFrame`'s scrolling column under it). Put it on the
 * field's modifier: `Modifier.fillMaxWidth().then(rememberKeptAboveKeyboard())`.
 */
@Composable
internal fun rememberKeptAboveKeyboard(): Modifier {
    val requester = remember { BringIntoViewRequester() }
    var focused by remember { mutableStateOf(false) }
    val keyboard = WindowInsets.ime.getBottom(LocalDensity.current)
    LaunchedEffect(focused, keyboard) {
        if (focused) requester.bringIntoView()
    }
    return remember(requester) {
        Modifier.bringIntoViewRequester(requester).onFocusChanged { focused = it.isFocused }
    }
}
