package com.hamzalabs.maktaba.desktop.shell

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.unit.dp
import com.hamzalabs.maktaba.core.api.componentsUrl
import com.hamzalabs.maktaba.desktop.LocalTexts
import com.hamzalabs.maktaba.desktop.Texts
import java.awt.Desktop
import java.net.URI

/** "Live TV is off on this home.": the feature's own sentence, else the general one with its id. */
fun Texts.featureOffSentence(feature: String): String =
    if (has("feature.off.$feature")) this["feature.off.$feature"] else this["feature.offAdmin", feature]

/** "Live TV isn't available on this home.": the feature's own sentence, else the general one. */
fun Texts.featureUnavailableSentence(feature: String): String =
    if (has("feature.unavailable.$feature")) this["feature.unavailable.$feature"] else this["feature.unavailable", feature]

/** Opens [url] in the system browser (nothing when this computer has none). */
fun openInBrowser(url: String) {
    runCatching { if (Desktop.isDesktopSupported()) Desktop.getDesktop().browse(URI(url)) }
}

/**
 * Where a feature that is off would be, for an admin (story 55.02): why, and a button that opens
 * the home's Components page in the browser. A viewer never sees it: the feature is hidden.
 */
@Composable
fun FeatureOffNote(feature: String, homeUrl: String, modifier: Modifier = Modifier, centered: Boolean = false, open: (String) -> Unit = ::openInBrowser) {
    val t = LocalTexts.current
    val note = @Composable {
        Surface(
            modifier.widthIn(max = 560.dp).fillMaxWidth().testTag("feature-off-$feature"),
            shape = RoundedCornerShape(12.dp),
            color = MaterialTheme.colorScheme.surfaceContainerHigh,
        ) {
            Column(Modifier.padding(16.dp), verticalArrangement = Arrangement.spacedBy(12.dp)) {
                Text(t.featureOffSentence(feature), style = MaterialTheme.typography.bodyLarge, textAlign = if (centered) TextAlign.Center else TextAlign.Start)
                OutlinedButton(onClick = { open(componentsUrl(homeUrl)) }, modifier = Modifier.testTag("feature-turn-on")) { Text(t["feature.turnOn"]) }
            }
        }
    }
    if (centered) Box(Modifier.fillMaxSize().padding(24.dp), contentAlignment = Alignment.Center) { note() } else note()
}
