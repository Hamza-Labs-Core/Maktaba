package com.hamzalabs.maktaba.player.ui.components

import androidx.activity.ComponentActivity
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.layout.wrapContentHeight
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.remember
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalConfiguration
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.test.junit4.createAndroidComposeRule
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.unit.Density
import androidx.compose.ui.unit.dp
import androidx.test.ext.junit.runners.AndroidJUnit4
import com.github.takahirom.roborazzi.RoborazziOptions
import com.github.takahirom.roborazzi.captureRoboImage
import com.hamzalabs.maktaba.core.ui.MaktabaTheme
import com.hamzalabs.maktaba.core.ui.ThemeMode
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import org.robolectric.annotation.GraphicsMode

/**
 * The components' screenshots (48.16, NFR-20): each component in the dark theme, English and
 * Arabic, at the normal font and at 200 % (Android's largest, as Dynamic Type AX3 on Apple,
 * NFR-7), on a phone. Pictures come from [FakePictures]; a URL with "missing" has no poster.
 * `recordRoborazziPhoneDebug` writes the references under `src/test/screenshots`; CI's
 * `verifyRoborazziPhoneDebug` fails when one no longer matches.
 */
@RunWith(AndroidJUnit4::class)
@GraphicsMode(GraphicsMode.Mode.NATIVE)
class ComponentsScreenshotTest {
    @get:Rule val compose = createAndroidComposeRule<ComponentActivity>()

    private fun shoot(name: String, fontScale: Float = 1f, content: @Composable (ar: Boolean) -> Unit) {
        compose.setContent {
            val ar = LocalConfiguration.current.locales[0].language == "ar"
            val d = LocalDensity.current
            val images = remember { FakePictures.loader(compose.activity) }
            CompositionLocalProvider(LocalDensity provides Density(d.density, fontScale), LocalHomeImages provides images) {
                MaktabaTheme(ThemeMode.Dark) {
                    Surface(Modifier.fillMaxWidth().wrapContentHeight().testTag("gallery")) {
                        Column(Modifier.padding(vertical = 16.dp)) { content(ar) }
                    }
                }
            }
        }
        compose.waitForIdle()
        val lang = if (compose.activity.resources.configuration.locales[0].language == "ar") "ar" else "en"
        val suffix = if (fontScale > 1f) "_font200" else ""
        compose.onNodeWithTag("gallery").captureRoboImage("src/test/screenshots/components_${name}_dark_$lang$suffix.png", roborazziOptions = options)
    }

    // ---------- the galleries ----------

    private val posterCards: @Composable (Boolean) -> Unit = { ar ->
        Row(Modifier.padding(horizontal = 16.dp), horizontalArrangement = Arrangement.spacedBy(12.dp)) {
            PosterCard(t(ar, "The Message", "الرسالة"), "https://home.test/api/videos/v1/poster", {}, subtitle = t(ar, "Films", "أفلام"), progress = 0.4f, width = 180.dp)
            PosterCard(t(ar, "Omar, Episode 1", "عمر، الحلقة ١"), "https://home.test/api/videos/v2/poster", {}, subtitle = t(ar, "Omar", "عمر"), watched = true, width = 180.dp)
        }
        Row(Modifier.padding(16.dp), horizontalArrangement = Arrangement.spacedBy(12.dp)) {
            PosterCard(t(ar, "Lion of the Desert, a long title that wraps", "أسد الصحراء، عنوان طويل يلتف على سطرين"), "https://home.test/missing", {}, width = 180.dp)
        }
    }

    private val progressStrips: @Composable (Boolean) -> Unit = { ar ->
        Column(Modifier.padding(horizontal = 16.dp), verticalArrangement = Arrangement.spacedBy(12.dp)) {
            listOf(0.1f, 0.4f, 0.95f).forEach { f ->
                Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(12.dp)) {
                    Text(t(ar, "${(f * 100).toInt()}%", "${(f * 100).toInt()}٪"), Modifier.width(56.dp), style = MaterialTheme.typography.labelLarge)
                    ProgressStrip(f, Modifier.width(240.dp))
                }
            }
        }
    }

    private val rail: @Composable (Boolean) -> Unit = { ar ->
        Rail(t(ar, "Continue Watching", "تابع المشاهدة"), onSeeAll = {}) {
            items(4) { n ->
                PosterCard(
                    t(ar, "Lesson ${n + 1}", "الدرس ${n + 1}"), "https://home.test/api/videos/r$n/poster", {},
                    subtitle = t(ar, "Seerah", "السيرة"), progress = 0.2f * (n + 1),
                )
            }
        }
    }

    private val hero: @Composable (Boolean) -> Unit = { ar ->
        Hero(
            t(ar, "Lion of the Desert", "أسد الصحراء"), "https://home.test/api/videos/v3/poster", {}, {},
            eyebrow = t(ar, "Featured", "مختار"), meta = t(ar, "1 h 30 min · 2024", "١ س ٣٠ د · ٢٠٢٤"), resume = true, progress = 0.3f,
        )
    }

    private val channelLogos: @Composable (Boolean) -> Unit = { ar ->
        Column(Modifier.padding(horizontal = 16.dp), verticalArrangement = Arrangement.spacedBy(12.dp)) {
            listOf(
                Triple(t(ar, "Sunna", "السنة"), "https://home.test/api/channels/c1/logo", "3"),
                Triple(t(ar, "Al Jazeera", "الجزيرة"), null, "12"),
                Triple(t(ar, "Quran TV", "قناة القرآن"), "https://home.test/missing-logo", null),
            ).forEach { (name, url, number) ->
                Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(12.dp)) {
                    ChannelLogo(name, url, number = number)
                    Text(name, style = MaterialTheme.typography.titleMedium)
                }
            }
        }
    }

    private val shimmer: @Composable (Boolean) -> Unit = { _ ->
        HeroPlaceholder(animate = false)
        RailPlaceholder(cards = 3, animate = false)
    }

    private fun t(ar: Boolean, en: String, arabic: String) = if (ar) arabic else en

    // ---------- the shots ----------

    @Test @Config(qualifiers = PHONE) fun posterCardEn() = shoot("postercard", content = posterCards)
    @Test @Config(qualifiers = "ar-$PHONE") fun posterCardAr() = shoot("postercard", content = posterCards)
    @Test @Config(qualifiers = PHONE) fun posterCardEnLarge() = shoot("postercard", LARGE, posterCards)
    @Test @Config(qualifiers = "ar-$PHONE") fun posterCardArLarge() = shoot("postercard", LARGE, posterCards)

    @Test @Config(qualifiers = PHONE) fun progressStripEn() = shoot("progressstrip", content = progressStrips)
    @Test @Config(qualifiers = "ar-$PHONE") fun progressStripAr() = shoot("progressstrip", content = progressStrips)
    @Test @Config(qualifiers = PHONE) fun progressStripEnLarge() = shoot("progressstrip", LARGE, progressStrips)
    @Test @Config(qualifiers = "ar-$PHONE") fun progressStripArLarge() = shoot("progressstrip", LARGE, progressStrips)

    @Test @Config(qualifiers = PHONE) fun railEn() = shoot("rail", content = rail)
    @Test @Config(qualifiers = "ar-$PHONE") fun railAr() = shoot("rail", content = rail)
    @Test @Config(qualifiers = PHONE) fun railEnLarge() = shoot("rail", LARGE, rail)
    @Test @Config(qualifiers = "ar-$PHONE") fun railArLarge() = shoot("rail", LARGE, rail)

    @Test @Config(qualifiers = PHONE) fun heroEn() = shoot("hero", content = hero)
    @Test @Config(qualifiers = "ar-$PHONE") fun heroAr() = shoot("hero", content = hero)
    @Test @Config(qualifiers = PHONE) fun heroEnLarge() = shoot("hero", LARGE, hero)
    @Test @Config(qualifiers = "ar-$PHONE") fun heroArLarge() = shoot("hero", LARGE, hero)

    @Test @Config(qualifiers = PHONE) fun channelLogoEn() = shoot("channellogo", content = channelLogos)
    @Test @Config(qualifiers = "ar-$PHONE") fun channelLogoAr() = shoot("channellogo", content = channelLogos)
    @Test @Config(qualifiers = PHONE) fun channelLogoEnLarge() = shoot("channellogo", LARGE, channelLogos)
    @Test @Config(qualifiers = "ar-$PHONE") fun channelLogoArLarge() = shoot("channellogo", LARGE, channelLogos)

    @Test @Config(qualifiers = PHONE) fun shimmerEn() = shoot("shimmer", content = shimmer)
    @Test @Config(qualifiers = "ar-$PHONE") fun shimmerAr() = shoot("shimmer", content = shimmer)
    @Test @Config(qualifiers = PHONE) fun shimmerEnLarge() = shoot("shimmer", LARGE, shimmer)
    @Test @Config(qualifiers = "ar-$PHONE") fun shimmerArLarge() = shoot("shimmer", LARGE, shimmer)

    private companion object {
        const val PHONE = "w411dp-h891dp-xxhdpi"
        /** Android's largest font size, 200 % (NFR-7). */
        const val LARGE = 2f
        // text is drawn by the host's font rasteriser: a reference recorded on a Mac still matches
        // on CI's Linux within a small share of pixels
        val options = RoborazziOptions(compareOptions = RoborazziOptions.CompareOptions(changeThreshold = 0.02f))
    }
}
