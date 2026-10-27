package com.hamzalabs.maktaba.core.ui

import android.os.Handler
import android.os.Looper
import androidx.camera.core.CameraSelector
import androidx.camera.core.ImageAnalysis
import androidx.camera.core.ImageProxy
import androidx.camera.core.Preview
import androidx.camera.lifecycle.ProcessCameraProvider
import androidx.camera.view.PreviewView
import androidx.compose.foundation.Canvas
import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.aspectRatio
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.safeDrawingPadding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Close
import androidx.compose.material.icons.outlined.QrCodeScanner
import androidx.compose.material3.Button
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.IconButtonDefaults
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberUpdatedState
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.geometry.Size
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.unit.dp
import androidx.compose.ui.viewinterop.AndroidView
import androidx.core.content.ContextCompat
import androidx.lifecycle.compose.LocalLifecycleOwner
import com.google.zxing.BarcodeFormat
import com.google.zxing.BinaryBitmap
import com.google.zxing.DecodeHintType
import com.google.zxing.EncodeHintType
import com.google.zxing.MultiFormatReader
import com.google.zxing.NotFoundException
import com.google.zxing.PlanarYUVLuminanceSource
import com.google.zxing.common.BitMatrix
import com.google.zxing.common.HybridBinarizer
import com.google.zxing.qrcode.QRCodeWriter
import com.google.zxing.qrcode.decoder.ErrorCorrectionLevel
import com.hamzalabs.maktaba.core.R
import com.hamzalabs.maktaba.core.permissions.AppPermissions
import com.hamzalabs.maktaba.core.permissions.PermissionAction
import com.hamzalabs.maktaba.core.permissions.PermissionState
import com.hamzalabs.maktaba.core.permissions.action
import com.hamzalabs.maktaba.core.permissions.ok
import com.hamzalabs.maktaba.core.permissions.rememberPermissionMemory
import com.hamzalabs.maktaba.core.permissions.rememberPermissions
import java.util.concurrent.Executors
import java.util.concurrent.atomic.AtomicBoolean

/**
 * [text] as a QR code: dark modules on white, with a quiet zone, whatever the theme (a scanner
 * reads dark on light). [description] is what TalkBack says.
 */
@Composable
fun QrCode(text: String, description: String, modifier: Modifier = Modifier) {
    val matrix = remember(text) { qrMatrix(text) }
    Canvas(
        modifier.aspectRatio(1f).clip(RoundedCornerShape(8.dp)).background(Color.White)
            .semantics { contentDescription = description }
            .testTag("qr-code"),
    ) {
        if (matrix == null) return@Canvas
        val cell = size.minDimension / matrix.width
        for (y in 0 until matrix.height) {
            for (x in 0 until matrix.width) {
                if (matrix[x, y]) drawRect(Color.Black, Offset(x * cell, y * cell), Size(cell + 0.5f, cell + 0.5f))
            }
        }
    }
}

/** The QR code's modules for [text] (medium error correction, a two-module quiet zone). */
internal fun qrMatrix(text: String): BitMatrix? = runCatching {
    QRCodeWriter().encode(
        text,
        BarcodeFormat.QR_CODE,
        0,
        0,
        mapOf(EncodeHintType.MARGIN to 2, EncodeHintType.ERROR_CORRECTION to ErrorCorrectionLevel.M, EncodeHintType.CHARACTER_SET to "UTF-8"),
    )
}.getOrNull()

/**
 * The camera, looking for a QR code: [onScan] gets the first one's text (once). Asks for the
 * camera when it opens (the walkthrough's rules: Android's dialog while it will show it, else
 * the app's settings page); without it, says so and offers [onClose] to type instead.
 * CameraX and ZXing's decoder: no Google Play services needed.
 */
@Composable
fun QrScanner(title: String, hint: String, onScan: (String) -> Unit, onClose: () -> Unit) {
    val permissions = rememberPermissions(listOf(AppPermissions.Camera), rememberPermissionMemory())
    val camera = permissions.rows.first()
    LaunchedEffect(Unit) {
        if (camera.state == PermissionState.NotAsked) permissions.act(AppPermissions.Camera)
    }
    Box(Modifier.fillMaxSize().background(Color.Black).testTag("qr-scanner")) {
        if (camera.state.ok) {
            CameraPreview(onScan)
            // the frame to aim with
            Box(
                Modifier.align(Alignment.Center).fillMaxWidth(0.7f).widthIn(max = 360.dp).aspectRatio(1f)
                    .border(3.dp, MaterialTheme.colorScheme.primary, RoundedCornerShape(20.dp)),
            )
        } else {
            Column(
                Modifier.align(Alignment.Center).padding(32.dp).widthIn(max = 420.dp),
                horizontalAlignment = Alignment.CenterHorizontally,
                verticalArrangement = Arrangement.spacedBy(12.dp),
            ) {
                Icon(Icons.Outlined.QrCodeScanner, null, tint = Color.White, modifier = Modifier.size(48.dp))
                Text(stringResource(R.string.camera_body), color = Color.White, textAlign = TextAlign.Center)
                if (camera.state == PermissionState.Denied || camera.state == PermissionState.DeniedPermanently) {
                    Text(stringResource(R.string.camera_denied), color = Color.White.copy(alpha = 0.8f), style = MaterialTheme.typography.bodySmall, textAlign = TextAlign.Center)
                }
                if (camera.state.action != PermissionAction.None) {
                    Button(onClick = { permissions.act(AppPermissions.Camera) }) {
                        Text(stringResource(if (camera.state.action == PermissionAction.OpenSettings) R.string.permission_open_settings else R.string.camera_allow))
                    }
                }
            }
        }
        Column(
            Modifier.align(Alignment.TopCenter).fillMaxWidth().safeDrawingPadding().background(Color.Black.copy(alpha = 0.55f)).padding(16.dp),
            verticalArrangement = Arrangement.spacedBy(4.dp),
        ) {
            Box(Modifier.fillMaxWidth()) {
                Text(title, style = MaterialTheme.typography.titleLarge, color = Color.White, modifier = Modifier.align(Alignment.CenterStart).padding(end = 48.dp))
                IconButton(
                    onClick = onClose,
                    modifier = Modifier.align(Alignment.CenterEnd).testTag("qr-close"),
                    colors = IconButtonDefaults.iconButtonColors(contentColor = Color.White),
                ) { Icon(Icons.Filled.Close, stringResource(R.string.close)) }
            }
            Text(hint, style = MaterialTheme.typography.bodyMedium, color = Color.White.copy(alpha = 0.85f))
        }
    }
}

/** The back camera (else the front one) with a QR decoder on its frames. */
@Composable
private fun CameraPreview(onScan: (String) -> Unit) {
    val context = LocalContext.current
    val owner = LocalLifecycleOwner.current
    val scanned by rememberUpdatedState(onScan)
    var unavailable by remember { mutableStateOf(false) }
    val executor = remember { Executors.newSingleThreadExecutor() }
    val provider = remember { ProcessCameraProvider.getInstance(context) }
    DisposableEffect(Unit) {
        onDispose {
            runCatching { if (provider.isDone) provider.get().unbindAll() }
            executor.shutdown()
        }
    }
    if (unavailable) {
        Text(
            stringResource(R.string.camera_unavailable),
            color = Color.White,
            textAlign = TextAlign.Center,
            modifier = Modifier.fillMaxSize().padding(32.dp),
        )
        return
    }
    AndroidView(
        modifier = Modifier.fillMaxSize(),
        factory = { ctx ->
            PreviewView(ctx).also { view ->
                provider.addListener({
                    val cameras = runCatching { provider.get() }.getOrNull() ?: run { unavailable = true; return@addListener }
                    val selector = when {
                        runCatching { cameras.hasCamera(CameraSelector.DEFAULT_BACK_CAMERA) }.getOrDefault(false) -> CameraSelector.DEFAULT_BACK_CAMERA
                        runCatching { cameras.hasCamera(CameraSelector.DEFAULT_FRONT_CAMERA) }.getOrDefault(false) -> CameraSelector.DEFAULT_FRONT_CAMERA
                        else -> { unavailable = true; return@addListener }
                    }
                    val preview = Preview.Builder().build().also { it.setSurfaceProvider(view.surfaceProvider) }
                    val analysis = ImageAnalysis.Builder()
                        .setBackpressureStrategy(ImageAnalysis.STRATEGY_KEEP_ONLY_LATEST)
                        .build()
                    val main = Handler(Looper.getMainLooper())
                    analysis.setAnalyzer(executor, QrAnalyzer { text -> main.post { scanned(text) } })
                    runCatching {
                        cameras.unbindAll()
                        cameras.bindToLifecycle(owner, selector, preview, analysis)
                    }.onFailure { unavailable = true }
                }, ContextCompat.getMainExecutor(ctx))
            }
        },
    )
}

/** Decodes QR codes from the camera's frames (their luminance plane), reporting the first once. */
private class QrAnalyzer(private val found: (String) -> Unit) : ImageAnalysis.Analyzer {
    private val done = AtomicBoolean(false)
    private val reader = MultiFormatReader().apply {
        setHints(mapOf(DecodeHintType.POSSIBLE_FORMATS to listOf(BarcodeFormat.QR_CODE), DecodeHintType.TRY_HARDER to true))
    }

    override fun analyze(image: ImageProxy) {
        image.use {
            if (done.get()) return
            val text = decode(it) ?: return
            if (done.compareAndSet(false, true)) found(text)
        }
    }

    private fun decode(image: ImageProxy): String? {
        val plane = image.planes.firstOrNull() ?: return null
        val buffer = plane.buffer.duplicate().apply { rewind() }
        val data = ByteArray(buffer.remaining()).also { buffer.get(it) }
        val stride = plane.rowStride
        val source = runCatching {
            PlanarYUVLuminanceSource(data, stride, image.height, 0, 0, image.width, image.height, false)
        }.getOrNull() ?: return null
        return try {
            reader.decodeWithState(BinaryBitmap(HybridBinarizer(source))).text
        } catch (_: NotFoundException) {
            // a code drawn light on dark
            runCatching { reader.decodeWithState(BinaryBitmap(HybridBinarizer(source.invert()))).text }.getOrNull()
        } catch (_: Exception) {
            null
        } finally {
            reader.reset()
        }
    }
}
