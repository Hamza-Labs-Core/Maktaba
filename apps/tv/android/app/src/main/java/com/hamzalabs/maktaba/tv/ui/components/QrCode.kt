package com.hamzalabs.maktaba.tv.ui.components

import androidx.compose.foundation.Canvas
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.runtime.Composable
import androidx.compose.runtime.remember
import androidx.compose.ui.Modifier
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.geometry.Size
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.dp
import com.google.zxing.BarcodeFormat
import com.google.zxing.EncodeHintType
import com.google.zxing.qrcode.QRCodeWriter
import com.google.zxing.qrcode.decoder.ErrorCorrectionLevel

/** The modules of [text]'s QR code: true is dark. Null when it does not fit in a QR. */
fun qrModules(text: String): Array<BooleanArray>? = runCatching {
    val hints = mapOf(EncodeHintType.ERROR_CORRECTION to ErrorCorrectionLevel.M, EncodeHintType.MARGIN to 0)
    val m = QRCodeWriter().encode(text, BarcodeFormat.QR_CODE, 0, 0, hints)
    Array(m.height) { y -> BooleanArray(m.width) { x -> m.get(x, y) } }
}.getOrNull()

/**
 * [text] as a QR code: black on white with a quiet zone (phones read dark on light), drawn
 * module by module so it stays sharp at any size.
 */
@Composable
fun QrCode(text: String, modifier: Modifier = Modifier, size: Dp = 240.dp) {
    val modules = remember(text) { qrModules(text) } ?: return
    Canvas(modifier.background(Color.White, CardCorner).padding(16.dp).size(size)) {
        val n = modules.size
        val cell = this.size.minDimension / n
        for (y in 0 until n) for (x in 0 until n) {
            if (modules[y][x]) drawRect(Color.Black, Offset(x * cell, y * cell), Size(cell + 0.5f, cell + 0.5f))
        }
    }
}
