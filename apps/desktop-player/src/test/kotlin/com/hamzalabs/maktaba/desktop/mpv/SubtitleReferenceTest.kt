package com.hamzalabs.maktaba.desktop.mpv

import org.junit.Assert.assertEquals
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test
import java.awt.image.BufferedImage
import java.io.File
import javax.imageio.ImageIO
import kotlin.math.abs

/**
 * A subtitle line's frame (a black picture with one line on it) against its reference, within
 * the line: the line is a thousandth of the frame, so a share of the whole frame would let half
 * of it go missing.
 */
internal object SubtitleReference {
    /** The share of the reference's lit pixels that may differ. */
    const val TOLERANCE = 0.05

    /** How far a channel may be off in a pixel that does not differ (a letter's edge, antialiased). */
    const val CHANNEL = 48

    /** A pixel of the line: brighter than the black picture and the line's own dark edges. */
    private const val LIT = 96

    private fun channels(p: Int) = intArrayOf(p shr 16 and 255, p shr 8 and 255, p and 255)

    private fun lit(p: Int) = channels(p).max() > LIT

    /** Why [img] is not [want] (null: it is): the pixels that differ within the box of the line, in either. */
    fun differs(want: BufferedImage, img: BufferedImage): String? {
        if (want.width != img.width || want.height != img.height) return "${img.width}x${img.height}, the reference ${want.width}x${want.height}"
        var count = 0
        var left = Int.MAX_VALUE
        var top = Int.MAX_VALUE
        var right = -1
        var bottom = -1
        for (y in 0 until want.height) for (x in 0 until want.width) {
            val inWant = lit(want.getRGB(x, y))
            if (inWant) count++
            if (!inWant && !lit(img.getRGB(x, y))) continue
            left = minOf(left, x)
            top = minOf(top, y)
            right = maxOf(right, x)
            bottom = maxOf(bottom, y)
        }
        if (count == 0) return "the reference has no line"
        var differ = 0
        for (y in top..bottom) for (x in left..right) {
            val a = channels(want.getRGB(x, y))
            val b = channels(img.getRGB(x, y))
            if ((0..2).any { abs(a[it] - b[it]) > CHANNEL }) differ++
        }
        val share = differ.toDouble() / count
        return if (share > TOLERANCE) "$differ pixels differ, ${"%.3f".format(share)} of the line's $count" else null
    }
}

/** [SubtitleReference] on the committed references themselves. */
class SubtitleReferenceTest {
    private fun reference(name: String): BufferedImage = ImageIO.read(File("src/test/resources/references/mpv-subs-$name.png"))

    /** [img] with each pixel as [change] makes it of its place and colour. */
    private fun changed(img: BufferedImage, change: (x: Int, y: Int, rgb: Int) -> Int): BufferedImage {
        val out = BufferedImage(img.width, img.height, BufferedImage.TYPE_INT_RGB)
        for (y in 0 until img.height) for (x in 0 until img.width) out.setRGB(x, y, change(x, y, img.getRGB(x, y) and 0xffffff))
        return out
    }

    private fun lit(rgb: Int) = maxOf(rgb shr 16 and 255, rgb shr 8 and 255, rgb and 255) > 96

    private fun columns(img: BufferedImage) = (0 until img.width).filter { x -> (0 until img.height).any { y -> lit(img.getRGB(x, y) and 0xffffff) } }

    @Test
    fun eachReferenceIsItself() {
        for (name in listOf("srt", "ass", "pgs", "sidecar")) {
            val r = reference(name)
            assertNull(name, SubtitleReference.differs(r, changed(r) { _, _, rgb -> rgb }))
        }
    }

    @Test
    fun halfALineMissingIsNotTheReference() {
        for (name in listOf("srt", "ass", "pgs", "sidecar")) {
            val r = reference(name)
            val cols = columns(r)
            val middle = (cols.first() + cols.last()) / 2
            assertNotNull("$name's left half", SubtitleReference.differs(r, changed(r) { x, _, rgb -> if (x < middle) 0 else rgb }))
            assertNotNull("$name's right half", SubtitleReference.differs(r, changed(r) { x, _, rgb -> if (x > middle) 0 else rgb }))
            assertNotNull("$name gone", SubtitleReference.differs(r, changed(r) { _, _, _ -> 0 }))
        }
        // the ASS line is the one a whole frame's tolerance (921 of its pixels) let half of go
        val ass = reference("ass")
        var all = 0
        for (y in 0 until ass.height) for (x in 0 until ass.width) if (lit(ass.getRGB(x, y) and 0xffffff)) all++
        assertTrue("$all lit", all / 2 < 921)
    }

    @Test
    fun anotherLineBesideOrElsewhereIsNotTheReference() {
        val r = reference("ass")
        // the line moved, its colour another, a second one over it
        assertNotNull("moved", SubtitleReference.differs(r, changed(r) { x, y, _ -> if (y >= 8) r.getRGB(x, y - 8) and 0xffffff else 0 }))
        assertNotNull("white", SubtitleReference.differs(r, changed(r) { _, _, rgb -> if (lit(rgb)) 0xffffff else rgb }))
        assertNotNull("twice", SubtitleReference.differs(r, changed(r) { x, y, rgb -> rgb or (if (y < 100) r.getRGB(x, y + 580) and 0xffffff else 0) }))
    }

    @Test
    fun aLittleAntialiasNoiseIsStillTheReference() {
        for (name in listOf("srt", "ass", "pgs", "sidecar")) {
            val r = reference(name)
            val rnd = java.util.Random(7)
            // every pixel of the line a little off, its edges too
            val noisy = changed(r) { _, _, rgb ->
                if (rgb == 0) return@changed 0
                fun off(v: Int) = (v + rnd.nextInt(61) - 30).coerceIn(0, 255)
                (off(rgb shr 16 and 255) shl 16) or (off(rgb shr 8 and 255) shl 8) or off(rgb and 255)
            }
            assertNull(name, SubtitleReference.differs(r, noisy))
        }
    }

    @Test
    fun anotherSizeIsNotTheReference() {
        assertEquals("640x360, the reference 1280x720", SubtitleReference.differs(reference("srt"), BufferedImage(640, 360, BufferedImage.TYPE_INT_RGB)))
    }
}
