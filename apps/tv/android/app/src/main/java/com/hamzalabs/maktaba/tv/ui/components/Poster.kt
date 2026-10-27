package com.hamzalabs.maktaba.tv.ui.components

import android.graphics.BitmapFactory
import android.util.LruCache
import androidx.compose.foundation.Image
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.ImageBitmap
import androidx.compose.ui.graphics.asImageBitmap
import androidx.compose.ui.layout.ContentScale
import androidx.tv.material3.MaterialTheme
import androidx.tv.material3.Text
import com.hamzalabs.maktaba.core.api.ApiClient
import com.hamzalabs.maktaba.tv.ui.theme.MaktabaPalette
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext

/** Posters already decoded, by home and video (a TV row scrolls back and forth a lot). */
private val posters = LruCache<String, ImageBitmap>(160)

/**
 * A video's picture. The home serves it only to a signed-in caller, so it is fetched with the
 * token through the core's client ([ApiClient.poster]), not by URL. Until it arrives (or when
 * the video has none) the card shows the gold ۞ on the surface colour.
 */
@Composable
fun Poster(client: ApiClient, video: String, modifier: Modifier = Modifier) {
    val key = "${client.home}|$video"
    var image by remember(key) { mutableStateOf(posters.get(key)) }
    LaunchedEffect(key) {
        if (image != null) return@LaunchedEffect
        val bytes = runCatching { client.poster(video) }.getOrNull() ?: return@LaunchedEffect
        val bmp = withContext(Dispatchers.Default) {
            val opts = BitmapFactory.Options().apply { inSampleSize = 2 }
            BitmapFactory.decodeByteArray(bytes, 0, bytes.size, opts)?.asImageBitmap()
        }
        if (bmp != null) {
            posters.put(key, bmp)
            image = bmp
        }
    }
    val img = image
    // the whole frame, as a player shows the video: a vertical or square one has black bars
    // at its sides, an ultra-wide one above and below
    Box(
        modifier.background(if (img != null) Color.Black else MaktabaPalette.Raised),
        contentAlignment = Alignment.Center,
    ) {
        if (img != null) {
            Image(img, contentDescription = null, contentScale = ContentScale.Fit, modifier = Modifier.fillMaxSize())
        } else {
            Text("۞", style = MaterialTheme.typography.headlineLarge, color = MaktabaPalette.Gold.copy(alpha = 0.5f))
        }
    }
}
