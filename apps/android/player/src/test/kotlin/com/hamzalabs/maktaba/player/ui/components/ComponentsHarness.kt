package com.hamzalabs.maktaba.player.ui.components

import android.content.Context
import android.graphics.Bitmap
import android.graphics.Canvas
import android.graphics.LinearGradient
import android.graphics.Paint
import android.graphics.Shader
import coil3.ImageLoader
import coil3.asImage
import coil3.decode.DataSource
import coil3.intercept.Interceptor
import coil3.request.ErrorResult
import coil3.request.ImageResult
import coil3.request.SuccessResult
import kotlinx.coroutines.Dispatchers
import java.io.ByteArrayOutputStream
import java.io.IOException

/**
 * Pictures for the components' tests without a network: an image loader that draws a fixed
 * picture for each URL at once (a URL containing "missing" fails, as a 404 does), so screenshots
 * are the same on every run and machine.
 */
object FakePictures {
    /** A loader answering every request from [picture], on the calling thread. */
    fun loader(context: Context): ImageLoader = ImageLoader.Builder(context)
        .memoryCache(null)
        .diskCache(null)
        .coroutineContext(Dispatchers.Unconfined)
        .interceptorCoroutineContext(Dispatchers.Unconfined)
        .components {
            add(
                Interceptor { chain ->
                    val url = chain.request.data.toString()
                    val result: ImageResult = if ("missing" in url) {
                        ErrorResult(null, chain.request, IOException("404"))
                    } else {
                        SuccessResult(picture(url).asImage(), chain.request, DataSource.MEMORY)
                    }
                    result
                },
            )
        }
        .build()

    /** A 16:9 picture (a square for a logo) whose colours follow [key]: gradient, sun and horizon. */
    fun picture(key: String, width: Int = 640, height: Int = if ("logo" in key) 640 else 360): Bitmap {
        val palettes = listOf(
            intArrayOf(0xFF3A2908.toInt(), 0xFFC8952C.toInt(), 0xFFF6DEA6.toInt()),
            intArrayOf(0xFF0F1725.toInt(), 0xFF1E5AD8.toInt(), 0xFF6F93FF.toInt()),
            intArrayOf(0xFF0B3B2A.toInt(), 0xFF0B6E3F.toInt(), 0xFF4CC38A.toInt()),
            intArrayOf(0xFF3B0B14.toInt(), 0xFFB6253A.toInt(), 0xFFF0707F.toInt()),
        )
        val p = palettes[Math.floorMod(key.hashCode(), palettes.size)]
        val bmp = Bitmap.createBitmap(width, height, Bitmap.Config.ARGB_8888)
        val c = Canvas(bmp)
        val paint = Paint(Paint.ANTI_ALIAS_FLAG)
        paint.shader = LinearGradient(0f, 0f, 0f, height.toFloat(), p[0], p[1], Shader.TileMode.CLAMP)
        c.drawRect(0f, 0f, width.toFloat(), height.toFloat(), paint)
        paint.shader = null
        paint.color = p[2]
        c.drawCircle(width * 0.7f, height * 0.4f, height * 0.18f, paint)
        paint.color = p[0]
        c.drawRect(0f, height * 0.72f, width.toFloat(), height.toFloat(), paint)
        return bmp
    }

    /** [picture] as PNG bytes, for a MockWebServer to serve. */
    fun png(key: String, width: Int = 1280, height: Int = 720): ByteArray =
        ByteArrayOutputStream().also { picture(key, width, height).compress(Bitmap.CompressFormat.PNG, 100, it) }.toByteArray()
}
