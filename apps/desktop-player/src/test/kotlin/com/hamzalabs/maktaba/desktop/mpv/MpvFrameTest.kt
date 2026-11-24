package com.hamzalabs.maktaba.desktop.mpv

import androidx.compose.ui.graphics.asComposeImageBitmap
import androidx.compose.ui.graphics.toAwtImage
import com.sun.jna.Pointer
import org.jetbrains.skia.Bitmap
import org.junit.Assert.assertEquals
import org.junit.Test

/** A frame mpv drew in `rgb0` (story 13.20): its fourth byte, 0 under a subtitle, is never read as alpha. */
class MpvFrameTest {
    @Test
    fun theFourthByteIsNoAlpha() {
        val b = Bitmap().also { it.allocPixels(frameInfo(2, 1)) }
        val at = Pointer(b.peekPixels()!!.use { it.addr })
        at.write(0L, byteArrayOf(0x11, 0x22, 0x33, 0, 0x44, 0x55, 0x66, 0xff.toByte()), 0, 8)
        b.notifyPixelsChanged()
        val img = b.asComposeImageBitmap().toAwtImage()
        assertEquals("ff112233", Integer.toHexString(img.getRGB(0, 0)))
        assertEquals("ff445566", Integer.toHexString(img.getRGB(1, 0)))
    }
}
