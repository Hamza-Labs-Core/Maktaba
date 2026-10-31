package com.hamzalabs.maktaba.remote

import android.content.res.Resources
import androidx.annotation.PluralsRes
import androidx.annotation.StringRes
import androidx.compose.runtime.Composable
import androidx.compose.runtime.remember
import androidx.compose.ui.platform.LocalConfiguration
import androidx.compose.ui.platform.LocalResources
import java.util.Locale

/**
 * Where the Remote's words come from: the app's string resources on a device (English or Arabic,
 * res/values and res/values-ar), the same files read by the unit tests on the JVM.
 */
interface Lookup {
    /** The language the words are in: numbers and dates are written in it too. */
    val locale: Locale

    fun text(@StringRes id: Int, vararg args: Any): String

    fun plural(@PluralsRes id: Int, count: Int, vararg args: Any): String
}

/** A resource whose `%n$s` take the named values the home sends, in this order. */
internal class Template(@StringRes val id: Int, val args: List<String> = emptyList())

/** The words of this device's language, from the app's resources. */
class ResourceLookup(private val res: Resources) : Lookup {
    override val locale: Locale get() = res.configuration.locales[0]

    override fun text(id: Int, vararg args: Any): String = if (args.isEmpty()) res.getString(id) else res.getString(id, *args)

    override fun plural(id: Int, count: Int, vararg args: Any): String = res.getQuantityString(id, count, *args)
}

/** The status screens' words for this composition's language: a language change re-reads them. */
@Composable
internal fun rememberSay(): Say {
    val resources = LocalResources.current
    val configuration = LocalConfiguration.current
    return remember(resources, configuration) { Say(ResourceLookup(resources)) }
}
