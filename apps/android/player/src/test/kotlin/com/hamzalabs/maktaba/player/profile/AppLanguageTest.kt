package com.hamzalabs.maktaba.player.profile

import android.app.Application
import android.app.LocaleManager
import android.os.LocaleList
import androidx.test.core.app.ApplicationProvider
import androidx.test.ext.junit.runners.AndroidJUnit4
import com.hamzalabs.maktaba.core.MapStore
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import org.xmlpull.v1.XmlPullParser

/**
 * FR-30's language: per-app locales on Android 13+ (`LocaleManager.setApplicationLocales`,
 * listed from the locale config AGP generates), an in-app choice before.
 */
@RunWith(AndroidJUnit4::class)
class AppLanguageTest {
    private val app: Application get() = ApplicationProvider.getApplicationContext()

    @Test
    fun aLanguageIsReadFromItsTags() {
        assertEquals(AppLanguage.System, AppLanguage.of(null))
        assertEquals(AppLanguage.System, AppLanguage.of(""))
        assertEquals(AppLanguage.Arabic, AppLanguage.of("ar-EG,en"))
        assertEquals(AppLanguage.English, AppLanguage.of("en-US"))
        assertEquals(AppLanguage.System, AppLanguage.of("fr"))
    }

    @Test
    @Config(sdk = [35])
    fun onAndroid13TheChoiceIsTheSystemsPerAppLocale() {
        val manager = app.getSystemService(LocaleManager::class.java)
        manager.applicationLocales = LocaleList.getEmptyLocaleList()
        val store = MapStore()
        val languages = AppLanguages(35, FrameworkLocales(app), store)
        assertTrue(languages.perAppLanguages)
        assertEquals(AppLanguage.System, languages.current.value)

        languages.set(AppLanguage.Arabic)
        assertEquals("ar", manager.applicationLocales.toLanguageTags())
        assertEquals(AppLanguage.Arabic, languages.current.value)
        // the system draws it: nothing kept or applied in the app
        assertEquals(AppLanguage.System, languages.applied.value)
        assertNull(store.get(AppLanguages.STORAGE_KEY))

        // changed in the system's settings: read again
        manager.applicationLocales = LocaleList.forLanguageTags("en")
        languages.refresh()
        assertEquals(AppLanguage.English, languages.current.value)

        languages.set(AppLanguage.System)
        assertTrue(manager.applicationLocales.isEmpty)
    }

    @Test
    fun beforeAndroid13TheChoiceIsKeptAndAppliedInTheApp() {
        val store = MapStore()
        val languages = AppLanguages(32, null, store)
        assertFalse(languages.perAppLanguages)
        languages.set(AppLanguage.Arabic)
        assertEquals("ar", store.get(AppLanguages.STORAGE_KEY))
        assertEquals(AppLanguage.Arabic, languages.applied.value)
        // kept across a restart
        assertEquals(AppLanguage.Arabic, AppLanguages(32, null, store).applied.value)
        languages.set(AppLanguage.System)
        assertNull(store.get(AppLanguages.STORAGE_KEY))
        assertEquals(AppLanguage.System, languages.applied.value)
    }

    @Test
    fun theGeneratedLocaleConfigListsEnglishAndArabic() {
        // AGP's generateLocaleConfig writes it and names it in the manifest (android:localeConfig)
        val id = app.resources.getIdentifier("_generated_res_locale_config", "xml", app.packageName)
        assertTrue("no generated locale config", id != 0)
        val locales = mutableListOf<String>()
        app.resources.getXml(id).use { xml ->
            while (xml.next() != XmlPullParser.END_DOCUMENT) {
                if (xml.eventType == XmlPullParser.START_TAG && xml.name == "locale") {
                    locales += xml.getAttributeValue("http://schemas.android.com/apk/res/android", "name")
                }
            }
        }
        assertEquals(setOf("en", "ar"), locales.map { java.util.Locale.forLanguageTag(it).language }.toSet())
    }
}
