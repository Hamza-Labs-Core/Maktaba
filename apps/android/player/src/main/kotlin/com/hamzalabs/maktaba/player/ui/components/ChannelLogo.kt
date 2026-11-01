package com.hamzalabs.maktaba.player.ui.components

import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.semantics.clearAndSetSemantics
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.tooling.preview.Preview
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.dp
import coil3.ImageLoader
import com.hamzalabs.maktaba.core.api.LiveApi
import com.hamzalabs.maktaba.core.api.LiveChannel
import com.hamzalabs.maktaba.core.ui.MaktabaTheme
import com.hamzalabs.maktaba.core.ui.MaktabaTokens
import com.hamzalabs.maktaba.core.ui.ThemeMode

/**
 * A channel's logo (Live TV's Now and Guide, FR-21 / FR-22): its picture fitted, uncropped, in a
 * rounded square on the raised surface; with none, the channel's [number] (or its name's first
 * letters) in brass, so every row still has a mark. TalkBack reads the channel's [name].
 */
@Composable
fun ChannelLogo(
    name: String,
    logoUrl: String?,
    modifier: Modifier = Modifier,
    number: String? = null,
    size: Dp = 48.dp,
    imageLoader: ImageLoader = homeImageLoader(),
) {
    val shape = RoundedCornerShape(MaktabaTokens.Radius.md)
    var missing by remember(logoUrl) { mutableStateOf(logoUrl == null) }
    Box(
        modifier
            .size(size)
            .clip(shape)
            .background(MaktabaTheme.brand.raised)
            .border(1.dp, MaterialTheme.colorScheme.outlineVariant, shape)
            .clearAndSetSemantics { contentDescription = name },
        contentAlignment = Alignment.Center,
    ) {
        if (missing) Mark(name, number)
        if (logoUrl != null) {
            HomePicture(
                logoUrl,
                Modifier.size(size).padding(size / 8),
                contentScale = ContentScale.Fit,
                imageLoader = imageLoader,
                missingFill = null,
                loadingColor = null,
                onState = { missing = it == PictureState.Missing },
            )
        }
    }
}

/** A [LiveChannel]'s logo, from where [api] says it loads (the home's logo route, or a URL). */
@Composable
fun ChannelLogo(channel: LiveChannel, api: LiveApi, modifier: Modifier = Modifier, size: Dp = 48.dp) =
    ChannelLogo(channel.name, api.logoUrl(channel)?.toString(), modifier, number = channel.number.takeIf { it > 0 }?.toString(), size = size)

/** The mark without a logo: the number, else up to two initials. */
@Composable
private fun Mark(name: String, number: String?) {
    Text(
        number ?: initials(name),
        style = MaterialTheme.typography.titleMedium,
        fontWeight = FontWeight.SemiBold,
        color = MaktabaTheme.brand.accentText,
        textAlign = TextAlign.Center,
        maxLines = 1,
        overflow = TextOverflow.Clip,
    )
}

/** Up to two first letters of [name]'s words ("Al Jazeera" → "AJ", «قناة الفجر» → «قا»). */
fun initials(name: String): String =
    name.split(' ', '-', '_').filter { it.isNotBlank() }.take(2).joinToString("") { it.take(1) }.uppercase().ifEmpty { "?" }

@Preview(name = "ChannelLogo", backgroundColor = 0xFF0B111C, showBackground = true)
@Composable
private fun ChannelLogoPreview() = MaktabaTheme(ThemeMode.Dark) {
    Row(Modifier.padding(16.dp)) {
        ChannelLogo("Al Jazeera", logoUrl = null, number = "12")
        ChannelLogo("Quran TV", logoUrl = null, Modifier.padding(start = 8.dp))
    }
}
