package com.hamzalabs.maktaba.desktop.home

import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.Button
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Brush
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.heading
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import com.hamzalabs.maktaba.core.api.Featured
import com.hamzalabs.maktaba.desktop.LocalTexts
import com.hamzalabs.maktaba.desktop.PosterImage
import com.hamzalabs.maktaba.desktop.Posters
import com.hamzalabs.maktaba.desktop.formatDuration

/**
 * Home's hero (story 55.03): the featured video or new series, its poster wide across the page,
 * its title, Play (or Resume from where the person left it) and More, which opens its page.
 */
@Composable
fun Hero(featured: Featured, posters: Posters, onPlay: () -> Unit, onMore: () -> Unit, modifier: Modifier = Modifier) {
    val t = LocalTexts.current
    val v = featured.video
    val series = featured.series?.takeIf { featured.kind == "series" }
    val title = series?.name?.takeIf { it.isNotBlank() } ?: v.displayTitle
    val resume = v.positionSec?.takeIf { it > 0 && v.completed != true }
    Box(modifier.fillMaxWidth().height(320.dp).padding(horizontal = 16.dp).clip(RoundedCornerShape(16.dp)).testTag("hero")) {
        PosterImage(posters, v.id, Modifier.fillMaxSize())
        Box(Modifier.fillMaxSize().background(Brush.verticalGradient(listOf(Color.Transparent, Color.Black.copy(alpha = 0.85f)))))
        Column(Modifier.align(Alignment.BottomStart).padding(24.dp).widthIn(max = 640.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
            Text(
                if (series != null) t["home.hero.newSeries"] else t["home.hero.featured"],
                color = MaterialTheme.colorScheme.primary, style = MaterialTheme.typography.labelLarge,
            )
            Text(
                title, color = Color.White, style = MaterialTheme.typography.headlineMedium, maxLines = 2, overflow = TextOverflow.Ellipsis,
                modifier = Modifier.semantics { heading() },
            )
            Row(horizontalArrangement = Arrangement.spacedBy(12.dp)) {
                Button(onClick = onPlay, modifier = Modifier.testTag("hero-play")) {
                    Text(resume?.let { t["video.resumeAt", formatDuration(it)] } ?: t["video.play"])
                }
                OutlinedButton(onClick = onMore, modifier = Modifier.testTag("hero-more")) { Text(t["home.moreOptions"]) }
            }
        }
    }
}
