package com.hamzalabs.maktaba.remote

import com.hamzalabs.maktaba.core.api.FailureGroup
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test
import java.time.ZoneOffset

/**
 * A Needs attention group says its cause in words: never the grouping key with its `kind:` and
 * its `<n>` / `<path>`, never a backslash-n, an Arabic file name in its own direction.
 */
class AttentionWordsTest {
    private val en = Say(XmlLookup.english, ZoneOffset.UTC)
    private val ar = Say(XmlLookup.arabic, ZoneOffset.UTC)

    private fun group(cause: String, label: String = cause, action: String = "retry") =
        FailureGroup(stage = "s", cause = cause, label = label, count = 1, action = action)

    @Test
    fun aGeneratedCauseIsTitledByItsKind() {
        val model = group("model_unavailable:<n> of <n> clips got no answer from the model")
        assertEquals("The model did not answer", en.groupTitle(model))
        assertEquals("لم يُجب النموذج", ar.groupTitle(model))
        val nas = group("nas_timeout:<path> محاضرات/سلسلة التاريخ..الجزء الثاني<n> | دروس عامة <n>.mp<n>>: no answer")
        assertEquals("NAS read timed out", en.groupTitle(nas))
        assertEquals("انتهت مهلة القراءة من NAS", ar.groupTitle(nas))
    }

    @Test
    fun aKnownCauseIsSaidInTheAppsLanguage() {
        assertEquals("Plex is unreachable", en.groupTitle(group("plex_unreachable", "Plex is unreachable")))
        assertEquals("لا يمكن الوصول إلى Plex", ar.groupTitle(group("plex_unreachable", "Plex is unreachable")))
    }

    @Test
    fun aCauseWithNoWordsHereTakesTheHomesLabelUnlessItIsAKey() {
        // a newer home's cause, labelled by the home (api queues/causes.go)
        assertEquals("Disk is read-only", en.groupTitle(group("disk_read_only", "Disk is read-only")))
        // an older home sent the key itself as the label: the kind in words instead
        assertEquals("Some new kind", en.groupTitle(group("some_new_kind:<n> things")))
        assertTrue(looksLikeKey("nas_timeout:<path>: no answer"))
        assertFalse(looksLikeKey("NAS read timed out"))
    }

    @Test
    fun aGroupRetryDoesNotFixSaysWhy() {
        assertNull(en.cannotRetry(group("nas_timeout")))
        assertEquals(
            "Retry does not fix this one: change the home's settings on the web.",
            en.cannotRetry(group("plex_unreachable", action = "open_settings")),
        )
        assertEquals("لا تُصلحه إعادة المحاولة: غيّر إعدادات الجهاز الرئيسي من الويب.", ar.cannotRetry(group("plex_unreachable", action = "open_settings")))
    }

    @Test
    fun aMessagesEscapesAreLineBreaks() {
        assertEquals(
            "return code -22 (Invalid argument)\n[out#0/image2 @ 0x6000]",
            readableMessage("return code -22 (Invalid argument)\\n[out#0/image2 @ 0x6000]\\n"),
        )
        assertFalse(readableMessage("a\\nb\\r\\nc").contains("\\"))
    }

    @Test
    fun theClipsCountIsSaidInTheAppsWordsWithItsPlural() {
        val msg = "4 of 4 clips got no answer from the model"
        assertEquals("4 of 4 clips got no answer from the model", en.sampleMessage(msg))
        assertEquals("1 of 1 clip got no answer from the model", en.sampleMessage("1 of 1 clips got no answer from the model"))
        // Arabic words and digits, no English left for the bidi algorithm to reorder
        assertEquals("لم يُجب النموذج عن ٤ من ٤ مقاطع", ar.sampleMessage(msg))
        assertEquals("لم يُجب النموذج عن ٣ من ١٢ مقطعًا", ar.sampleMessage("3 of 12 clips got no answer from the model"))
        assertEquals("لم يُجب النموذج عن ١ من مقطع واحد", ar.sampleMessage("1 of 1 clips got no answer from the model"))
        assertEquals("لم يُجب النموذج عن ٢ من مقطعين", ar.sampleMessage("2 of 2 clips got no answer from the model"))
        // any other message as the home wrote it, made readable
        assertEquals("read failed: head, tail", ar.sampleMessage("read failed: head, tail"))
    }

    @Test
    fun theBarSaysTheSameWordAsTheTitle() {
        // one word on the bar, the title's own: the web's and Apple's Needs attention
        assertEquals("Needs attention", en.t(R.string.place_attention))
        assertEquals("ما يحتاج إلى انتباه", ar.t(R.string.place_attention))
        for (say in listOf(en, ar)) {
            val short = say.t(R.string.place_attention_short)
            assertFalse(short.contains(' '))
            assertTrue("$short in ${say.t(R.string.place_attention)}", say.t(R.string.place_attention).contains(short, ignoreCase = true))
        }
    }

    @Test
    fun anArabicFileNameInAnEnglishLineIsIsolated() {
        val line = "/Volumes/Media/محاضرات/الدرس.mp4: no answer"
        assertEquals("⁨/Volumes/Media/محاضرات/الدرس.mp4⁩: no answer", isolateRtl(line))
        // nothing right-to-left: as it was
        assertEquals("read failed: head, tail", isolateRtl("read failed: head, tail"))
    }
}
