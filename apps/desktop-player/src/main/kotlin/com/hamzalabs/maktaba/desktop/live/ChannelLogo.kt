package com.hamzalabs.maktaba.desktop.live

import androidx.compose.foundation.Image
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.produceState
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.ImageBitmap
import androidx.compose.ui.graphics.toComposeImageBitmap
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.unit.Dp
import com.hamzalabs.maktaba.core.api.LiveApi
import com.hamzalabs.maktaba.core.api.LiveChannel
import org.jetbrains.skia.Image as SkiaImage

/** Logos already fetched, by channel. */
private val logos = object : LinkedHashMap<String, ImageBitmap?>(32, 0.75f, true) {
    override fun removeEldestEntry(eldest: MutableMap.MutableEntry<String, ImageBitmap?>) = size > 200
}

/**
 * A channel's logo (story 55.07): its picture from the home's logo route, fitted; a channel
 * with no logo shows its number in a circle.
 */
@Composable
fun ChannelLogo(channel: LiveChannel, api: LiveApi, size: Dp, modifier: Modifier = Modifier) {
    val logo by produceState<ImageBitmap?>(null, channel.id) {
        if (channel.logoUrl.isNullOrBlank()) return@produceState
        val key = "${api.client.home}|${channel.id}"
        synchronized(logos) { if (logos.containsKey(key)) { value = logos[key]; return@produceState } }
        val img = runCatching { SkiaImage.makeFromEncoded(api.logo(channel.id)).toComposeImageBitmap() }.getOrNull()
        synchronized(logos) { logos[key] = img }
        value = img
    }
    Box(
        modifier.size(size).clip(CircleShape).background(MaterialTheme.colorScheme.surfaceContainerHigh).semantics { contentDescription = channel.name },
        contentAlignment = Alignment.Center,
    ) {
        val l = logo
        if (l != null) Image(l, null, contentScale = ContentScale.Fit, modifier = Modifier.size(size).padding(size / 8))
        else Text(if (channel.number > 0) "${channel.number}" else channel.name.take(2), color = MaterialTheme.colorScheme.primary, style = MaterialTheme.typography.titleSmall)
    }
}
