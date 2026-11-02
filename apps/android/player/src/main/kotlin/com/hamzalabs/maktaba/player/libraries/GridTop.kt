package com.hamzalabs.maktaba.player.libraries

import androidx.compose.foundation.lazy.grid.LazyGridState
import androidx.compose.runtime.Composable
import androidx.compose.runtime.SideEffect
import androidx.compose.runtime.remember

/**
 * A grid the person hasn't scrolled stays at its top when [key] (what comes first in it) changes.
 *
 * A lazy grid keeps its first visible item's key in place: when items come in above it (a
 * library's hub rows answering after its videos), or it moves down (the first pinned library
 * unpinned), the grid scrolls to keep showing it, or as far as its end allows, and opens part
 * way down. At the top, the top is what the person sees, so the next measure starts at the
 * first item again instead. A grid scrolled anywhere else keeps its key, as before.
 *
 * Call it in composition with what the grid's first items are made of; the check runs once
 * the change is applied, before the grid measures it.
 */
@Composable
internal fun LazyGridState.StaysAtTopWhen(key: Any?) {
    val seen = remember { arrayOf(key) }
    SideEffect {
        if (seen[0] != key) {
            seen[0] = key
            if (firstVisibleItemIndex == 0 && firstVisibleItemScrollOffset == 0) requestScrollToItem(0)
        }
    }
}
