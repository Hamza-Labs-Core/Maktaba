package com.hamzalabs.maktaba.tv.ui.screens

import android.graphics.BitmapFactory
import android.util.LruCache
import androidx.compose.foundation.Image
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.fillMaxHeight
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableLongStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.ImageBitmap
import androidx.compose.ui.graphics.asImageBitmap
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.platform.LocalConfiguration
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.dp
import androidx.tv.material3.MaterialTheme
import androidx.tv.material3.Text
import com.hamzalabs.maktaba.core.api.LiveApi
import com.hamzalabs.maktaba.core.api.LiveChannel
import com.hamzalabs.maktaba.core.api.ServerTimed
import com.hamzalabs.maktaba.tv.ui.theme.MaktabaPalette
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.delay
import kotlinx.coroutines.withContext
import java.time.Instant
import java.time.ZoneId
import java.time.format.DateTimeFormatter
import java.time.format.FormatStyle

// Pieces the Live TV screens share (Epic 48 story 48.35): a channel's logo, the clock the guide
// reads (the home's, so a TV whose clock is wrong still shows the right programme), times.

/** An answer and this TV's clock when it came: the home's time now follows from the two. */
data class Timed<T : ServerTimed>(val value: T, val atLocalMs: Long = System.currentTimeMillis())

/** The home's time now, from [answer]'s clock, moving on every [tickMs]. */
@Composable
fun rememberServerNow(answer: Timed<*>?, tickMs: Long = 15_000): Long {
    var local by remember { mutableLongStateOf(System.currentTimeMillis()) }
    LaunchedEffect(Unit) {
        while (true) {
            delay(tickMs)
            local = System.currentTimeMillis()
        }
    }
    return answer?.value?.serverNow(local, answer.atLocalMs) ?: local
}

/** "21:30" (or "9:30 PM"), in the app's language and this TV's time zone. */
@Composable
fun rememberTimeFormat(): (Long) -> String {
    val locale = LocalConfiguration.current.locales[0]
    return remember(locale) {
        val f = DateTimeFormatter.ofLocalizedTime(FormatStyle.SHORT).withLocale(locale).withZone(ZoneId.systemDefault())
        ({ ms: Long -> f.format(Instant.ofEpochMilli(ms)) })
    }
}

/** Logos already decoded, by home and channel. */
private val logos = LruCache<String, ImageBitmap>(64)

/**
 * A channel's logo, fetched with the token ([LiveApi.logo]; the home serves it only to a
 * signed-in caller), else its number on the raised surface.
 */
@Composable
fun ChannelBadge(live: LiveApi, channel: LiveChannel, size: Dp = 56.dp, modifier: Modifier = Modifier) {
    val key = "${live.client.home}|${channel.id}"
    var image by remember(key) { mutableStateOf(logos.get(key)) }
    LaunchedEffect(key) {
        if (image != null || channel.logoUrl.isNullOrBlank()) return@LaunchedEffect
        val bytes = try {
            live.logo(channel.id)
        } catch (e: CancellationException) {
            throw e
        } catch (_: Exception) {
            return@LaunchedEffect // the number stands in for it
        }
        val bmp = withContext(Dispatchers.Default) { BitmapFactory.decodeByteArray(bytes, 0, bytes.size)?.asImageBitmap() }
        if (bmp != null) {
            logos.put(key, bmp)
            image = bmp
        }
    }
    val shape = RoundedCornerShape(10.dp)
    Box(modifier.size(size).clip(shape).background(MaktabaPalette.Raised), contentAlignment = Alignment.Center) {
        val img = image
        if (img != null) {
            Image(img, contentDescription = null, contentScale = ContentScale.Fit, modifier = Modifier.padding(6.dp))
        } else {
            Text(channel.number.toString(), style = MaterialTheme.typography.titleLarge, color = MaktabaPalette.Gold)
        }
    }
}

/** How far through the programme on air is: a thin gold bar on the raised surface. */
@Composable
fun LiveProgress(fraction: Double, modifier: Modifier = Modifier) {
    Box(modifier.fillMaxWidth().height(5.dp).clip(RoundedCornerShape(3.dp)).background(MaktabaPalette.Raised)) {
        Box(Modifier.fillMaxHeight().fillMaxWidth(fraction.toFloat().coerceIn(0f, 1f)).background(MaktabaPalette.Gold))
    }
}
