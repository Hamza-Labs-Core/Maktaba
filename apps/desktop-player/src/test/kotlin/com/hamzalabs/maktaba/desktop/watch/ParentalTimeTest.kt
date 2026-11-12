package com.hamzalabs.maktaba.desktop.watch

import com.hamzalabs.maktaba.core.api.ApiException
import com.hamzalabs.maktaba.desktop.Lang
import com.hamzalabs.maktaba.desktop.Texts
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test
import java.time.Instant
import java.time.ZoneId

/** Epic 57 story 57.15 (T05, T06): the home's `parental-time` refusal as the desktop Player reads it. */
class ParentalTimeTest {
    private val problem = """{"type":"https://maktaba.tv/problems/parental-time","title":"watching time is over",""" +
        """"status":403,"code":"parental_time","reason":"limit_reached","next_allowed_at":"2026-11-12T04:00:00Z"}"""

    @Test
    fun readsTheReasonAndWhenWatchingOpensAgain() {
        val up = ParentalTime.refusal(ApiException.Status(403, problem))
        assertEquals(TimeUp("limit_reached", Instant.parse("2026-11-12T04:00:00Z")), up)
    }

    @Test
    fun knowsTheRefusalByItsTypeOrByItsCode() {
        val byType = """{"type":"https://example.invalid/problems/parental-time","status":403,"reason":"outside_slot"}"""
        assertEquals(TimeUp("outside_slot", null), ParentalTime.refusal(ApiException.Status(403, byType)))
        val byCode = """{"type":"about:blank","status":403,"code":"parental_time"}"""
        assertEquals(TimeUp("", null), ParentalTime.refusal(ApiException.Status(403, byCode)))
    }

    @Test
    fun anyOtherAnswerIsNotATimeRefusal() {
        assertNull(ParentalTime.refusal(ApiException.Status(403, """{"type":"about:blank","status":403,"code":"restricted"}""")))
        assertNull(ParentalTime.refusal(ApiException.Status(404, problem)))
        assertNull(ParentalTime.refusal(ApiException.Status(403, "<html>forbidden</html>")))
        assertNull(ParentalTime.refusal(IllegalStateException("boom")))
    }

    @Test
    fun aNextTimeThatIsNotATimeIsLeftOut() {
        val bad = """{"type":"https://maktaba.tv/problems/parental-time","status":403,"reason":"limit_reached","next_allowed_at":"soon"}"""
        assertEquals(TimeUp("limit_reached", null), ParentalTime.refusal(ApiException.Status(403, bad)))
    }

    @Test
    fun theTitleSaysWhy() {
        assertEquals("parental.time.outside.title", ParentalTime.titleKey("outside_slot"))
        assertEquals("parental.time.up.title", ParentalTime.titleKey("limit_reached"))
        assertEquals("parental.time.up.title", ParentalTime.titleKey("no_heartbeat"))
        assertEquals("parental.time.up.title", ParentalTime.titleKey(""))
    }

    @Test
    fun theNextTimeIsTheTimeAloneTodayAndNamesTheDayOtherwise() {
        val zone = ZoneId.of("UTC")
        val now = Instant.parse("2026-11-11T18:00:00Z")
        val today = ParentalTime.next(Instant.parse("2026-11-11T20:30:00Z"), Lang.En, zone, now)
        assertTrue(today, today.contains("8:30") && !today.contains("Wednesday"))
        val tomorrow = ParentalTime.next(Instant.parse("2026-11-12T07:00:00Z"), Lang.En, zone, now)
        assertTrue(tomorrow, tomorrow.startsWith("Thursday ") && tomorrow.contains("7:00"))
        val arabic = ParentalTime.next(Instant.parse("2026-11-12T07:00:00Z"), Lang.Ar, zone, now)
        assertTrue(arabic, arabic.startsWith("الخميس "))
    }

    @Test
    fun aPositionReadsAsAClock() {
        assertEquals("0:00", ParentalTime.clock(0))
        assertEquals("12:34", ParentalTime.clock(754_000))
        assertEquals("1:02:03", ParentalTime.clock(3_723_000))
    }

    @Test
    fun theScreenHasItsWordsInEnglishAndArabic() {
        val en = Texts(Lang.En)
        val ar = Texts(Lang.Ar)
        assertEquals("Time's up for today", en["parental.time.up.title"])
        assertEquals("Not watching time yet", en["parental.time.outside.title"])
        assertEquals("You can watch again at 8:30 PM", en["parental.time.next", "8:30 PM"])
        assertEquals("Ask a parent for more time", en["parental.time.askParent"])
        assertEquals("انتهى وقت اليوم", ar["parental.time.up.title"])
        assertEquals("لم يحن وقت المشاهدة بعد", ar["parental.time.outside.title"])
        assertEquals("اطلب من أحد والديك وقتًا إضافيًا", ar["parental.time.askParent"])
        assertEquals("تحقّق مجددًا", ar["parental.time.check"])
        for (k in listOf("parental.time.next", "parental.time.saved")) assertTrue(k, ar[k, "X"].contains("X"))
    }

    @Test
    fun aLengthOfTimeInWordsInBothLanguages() {
        val en = Texts(Lang.En)
        val ar = Texts(Lang.Ar)
        assertEquals("less than a minute", ParentalTime.words(59, en))
        assertEquals("1 minute", ParentalTime.words(60, en))
        assertEquals("14 minutes", ParentalTime.words(899, en))
        assertEquals("1 hour", ParentalTime.words(3600, en))
        assertEquals("1 hour and 5 minutes", ParentalTime.words(3900, en))
        assertEquals("أقل من دقيقة", ParentalTime.words(10, ar))
        assertEquals("دقيقة واحدة", ParentalTime.words(60, ar))
        assertEquals("دقيقتان", ParentalTime.words(120, ar))
        assertEquals("5 دقائق", ParentalTime.words(300, ar))
        assertEquals("15 دقيقة", ParentalTime.words(900, ar))
        assertEquals("ساعة واحدة و30 دقيقة", ParentalTime.words(5400, ar))
        assertEquals("12 minutes left today", en["parental.time.left", ParentalTime.words(720, en)])
        assertEquals("Watching ends in 5 minutes.", en["parental.time.warning", ParentalTime.words(300, en)])
        assertEquals("تنتهي المشاهدة بعد دقيقة واحدة.", ar["parental.time.warning", ParentalTime.words(60, ar)])
    }

    @Test
    fun arabicPluralFormsFollowTheNumber() {
        assertEquals(listOf("zero", "one", "two", "few", "few", "many", "many", "other", "other", "few"),
            listOf(0, 1, 2, 3, 10, 11, 99, 100, 102, 103).map { ParentalTime.plural(Lang.Ar, it) })
        assertEquals(listOf("other", "one", "other"), listOf(0, 1, 2).map { ParentalTime.plural(Lang.En, it) })
    }
}
