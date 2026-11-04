package com.hamzalabs.maktaba.core

import com.hamzalabs.maktaba.core.ui.featureName
import com.hamzalabs.maktaba.core.ui.featureOff
import com.hamzalabs.maktaba.core.ui.featureUnavailable
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test
import java.io.File

/**
 * A feature that is off reads as its own whole sentence (story 51.11), so the verb agrees with the
 * name: "Transcripts are off", «البث المباشر متوقف». Every feature the app names has both sentences.
 * That they are the web's words (capabilities.off.<id>, capabilities.unavailable.<id>) is checked
 * by tools/ci/catalog_words.mjs, for every platform at once.
 */
class FeatureWordsTest {
    // the tests run in apps/android/core
    private val res = File("src/main/res")

    private val features = listOf(
        "live_tv", "transcripts", "subtitles", "semantic_search", "chapters", "youtube", "plex", "remote_access", "recommendations",
    )

    @Test
    fun everyNamedFeatureHasBothSentences() {
        for (f in features) {
            assertNotNull(f, featureName(f))
            assertNotNull(f, featureOff(f))
            assertNotNull(f, featureUnavailable(f))
        }
        assertNull(featureOff("from_the_future"))
    }

    @Test
    fun aPluralOrMasculineNameAgrees() {
        val en = StringsXml(File(res, "values/strings.xml")).strings
        val ar = StringsXml(File(res, "values-ar/strings.xml")).strings
        assertEquals("Transcripts are off on this home.", en["feature_off_transcripts"])
        assertEquals("Chapters aren't available on this home.", en["feature_unavailable_chapters"])
        assertEquals("البث المباشر متوقف على الجهاز الرئيسي.", ar["feature_off_live_tv"])
        assertEquals("نصوص الفيديو متوقفة على الجهاز الرئيسي.", ar["feature_off_transcripts"])
        // the frame for a feature this app has no sentence for: its head noun carries the agreement
        assertEquals("The %1\$s feature is off on this home.", en["feature_off_admin"])
        assertTrue(ar.getValue("feature_off_admin").startsWith("ميزة %1\$s متوقفة"))
    }
}
