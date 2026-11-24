package com.hamzalabs.maktaba.desktop.shell

import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.background
import androidx.compose.material3.Button
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.toAwtImage
import androidx.compose.ui.test.ExperimentalTestApi
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.performClick
import androidx.compose.ui.test.runDesktopComposeUiTest
import com.hamzalabs.maktaba.desktop.Lang
import com.hamzalabs.maktaba.desktop.PlayerTheme
import com.hamzalabs.maktaba.desktop.Texts
import org.junit.Assert.fail
import org.junit.Test
import java.awt.image.BufferedImage
import java.io.File
import javax.imageio.ImageIO
import kotlin.math.abs

/**
 * Story 13.15's visual references: the Player's title bar, its menu open, and its dialog, dark,
 * in English and Arabic, against src/test/resources/references/ (docs/design/desktop/README.md,
 * "Quality bar"). They are compared on Linux, where CI takes them (_desktop-player.yml's linux
 * leg, the fonts the Player carries); elsewhere each render is only written to
 * build/frame-references/ for a look. A missing reference is written to
 * build/new-references/ and fails the test: CI uploads it for the pull request to commit.
 * Test data only, no media.
 */
@OptIn(ExperimentalTestApi::class)
class FrameReferencesTest {
    private val onLinux = System.getProperty("os.name").orEmpty().startsWith("Linux")

    private fun menu(lang: Lang) = FrameMenu(
        signedInLine = if (lang == Lang.Ar) "مسجّل الدخول باسم haddad في الجهاز الرئيسي" else "Signed in as haddad on Living room",
        approve = {},
        crashReports = (if (lang == Lang.Ar) "إرسال تقارير الأعطال إلى الجهاز الرئيسي" else "Send crash reports to Living room") to true,
        signOut = {},
        switchHome = {},
        lang = lang,
        reportProblem = {},
        myReports = {},
    )

    private fun shoot(name: String, width: Int, height: Int, open: Boolean = false, content: @Composable () -> Unit) =
        runDesktopComposeUiTest(width, height) {
            setContent { Box(Modifier.fillMaxSize().background(Color(0xFF0B111C))) { content() } }
            if (open) onNodeWithTag("frame-menu").performClick()
            waitForIdle()
            check(name, captureToImage().toAwtImage())
        }

    @Test fun titleBarEnglish() = shoot("player-frame-bar-en", 1280, 44) { bar(Lang.En) }
    @Test fun titleBarArabic() = shoot("player-frame-bar-ar", 1280, 44) { bar(Lang.Ar) }
    @Test fun menuEnglish() = shoot("player-frame-menu-en", 480, 760, open = true) { bar(Lang.En) }
    @Test fun menuArabic() = shoot("player-frame-menu-ar", 480, 760, open = true) { bar(Lang.Ar) }
    @Test fun dialogEnglish() = shoot("player-dialog-en", 640, 320) { dialog(Lang.En) }
    @Test fun dialogArabic() = shoot("player-dialog-ar", 640, 320) { dialog(Lang.Ar) }

    @Composable
    private fun bar(lang: Lang) {
        val texts = Texts(lang)
        PlayerTheme(texts, big = false) { FrameBar(texts, menu(lang), drawBackground = true) }
    }

    @Composable
    private fun dialog(lang: Lang) {
        val ar = lang == Lang.Ar
        PlayerTheme(Texts(lang), big = false) {
            MaktabaAlertDialog(
                onDismissRequest = {},
                title = { Text(if (ar) "تسجيل الخروج من الجهاز الرئيسي؟" else "Sign out of Living room?") },
                text = { Text(if (ar) "يمكنك تسجيل الدخول مجددًا بكلمة المرور أو برمز." else "You can sign in again with your password or a code.") },
                dismissButton = { TextButton(onClick = {}) { Text(if (ar) "إلغاء" else "Cancel") } },
                confirmButton = { Button(onClick = {}) { Text(if (ar) "تسجيل الخروج" else "Sign out") } },
            )
        }
    }

    private fun check(name: String, actual: BufferedImage) {
        val build = File("build")
        File(build, "frame-references").also { it.mkdirs() }.let { ImageIO.write(actual, "png", File(it, "$name.png")) }
        if (!onLinux) return
        val ref = File("src/test/resources/references/$name.png")
        if (!ref.exists()) {
            File(build, "new-references").also { it.mkdirs() }.let { ImageIO.write(actual, "png", File(it, "$name.png")) }
            fail("$name: no committed reference; this run wrote build/new-references/$name.png (look at it, commit it, list it under Reference images:)")
        }
        val want = ImageIO.read(ref)
        if (want.width != actual.width || want.height != actual.height) fail("$name: ${actual.width}×${actual.height}, the reference is ${want.width}×${want.height}")
        var differ = 0
        for (y in 0 until want.height) for (x in 0 until want.width) {
            val a = want.getRGB(x, y)
            val b = actual.getRGB(x, y)
            val d = maxOf(abs((a shr 16 and 255) - (b shr 16 and 255)), abs((a shr 8 and 255) - (b shr 8 and 255)), abs((a and 255) - (b and 255)))
            if (d > 16) differ++
        }
        val ratio = differ.toDouble() / (want.width * want.height)
        if (ratio > 0.001) {
            File(build, "reference-diffs").also { it.mkdirs() }.let { ImageIO.write(actual, "png", File(it, "$name.png")) }
            fail("$name: ${"%.4f".format(ratio)} of the pixels differ from the reference (build/reference-diffs/$name.png)")
        }
    }
}
