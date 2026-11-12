package com.hamzalabs.maktaba.desktop.watch

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.padding
import androidx.compose.material3.Button
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.LiveRegionMode
import androidx.compose.ui.semantics.heading
import androidx.compose.ui.semantics.liveRegion
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.unit.dp
import com.hamzalabs.maktaba.desktop.LocalTexts

/**
 * The time's-up screen (Epic 57 story 57.15): why the video stopped, when watching opens
 * again, where it stopped, and that a parent can give more time. Playback has stopped and the
 * home is not asked again until the viewer chooses Check again.
 */
@Composable
fun TimesUp(up: TimeUp, onCheck: () -> Unit) {
    val t = LocalTexts.current
    Column(
        Modifier.padding(24.dp).testTag("parental-times-up").semantics { liveRegion = LiveRegionMode.Polite },
        horizontalAlignment = Alignment.CenterHorizontally,
        verticalArrangement = Arrangement.spacedBy(12.dp),
    ) {
        Text(
            t[ParentalTime.titleKey(up.reason)], color = Color.White,
            style = MaterialTheme.typography.headlineSmall, modifier = Modifier.semantics { heading() },
        )
        up.nextAllowedAt?.let { Text(t["parental.time.next", ParentalTime.next(it, t.lang)], color = Color.White) }
        if (up.positionMs >= 1000) Text(t["parental.time.saved", ParentalTime.clock(up.positionMs)], color = Color.White)
        Text(t["parental.time.askParent"], color = Color.White)
        Button(onClick = onCheck, modifier = Modifier.testTag("parental-time-check")) { Text(t["parental.time.check"]) }
    }
}
