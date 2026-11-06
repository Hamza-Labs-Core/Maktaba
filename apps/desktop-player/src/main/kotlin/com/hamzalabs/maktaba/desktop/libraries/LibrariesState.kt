package com.hamzalabs.maktaba.desktop.libraries

import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import com.hamzalabs.maktaba.core.api.ApiClient
import com.hamzalabs.maktaba.core.api.ApiException
import com.hamzalabs.maktaba.core.api.LibrarySummary
import com.hamzalabs.maktaba.core.api.librarySummaries
import com.hamzalabs.maktaba.core.api.setPinnedLibraries
import com.hamzalabs.maktaba.core.session.ReadOrder
import kotlinx.coroutines.CancellationException

/**
 * Runs [block] the way every page of the section loads: a sign-out goes to the start screen, a
 * cancel stays a cancel, anything else is kept to show ([onFailure]).
 */
internal suspend fun loading(signedOut: () -> Unit, onFailure: (Throwable) -> Unit, block: suspend () -> Unit) {
    try {
        block()
    } catch (e: ApiException.SignedOut) {
        signedOut()
    } catch (e: CancellationException) {
        throw e
    } catch (e: Exception) {
        onFailure(e)
    }
}

/** The pinned first, in their pin order, then the rest in [order] (as the Android Player's). */
fun pinnedFirst(libraries: List<LibrarySummary>, order: List<String> = libraries.map { it.id }): List<LibrarySummary> {
    val (pinned, rest) = libraries.partition { it.pinned }
    val at = { id: String -> order.indexOf(id).let { i -> if (i < 0) Int.MAX_VALUE else i } }
    return pinned.sortedBy { it.pinPosition ?: Int.MAX_VALUE } + rest.sortedBy { at(it.id) }
}

/** The pins after toggling [id]: unpinned if pinned, else pinned at the end. */
fun togglePins(libraries: List<LibrarySummary>, id: String): List<String> {
    val pins = pinnedFirst(libraries).filter { it.pinned }.map { it.id }
    return if (id in pins) pins - id else pins + id
}

/**
 * The Libraries section (story 55.05): the library cards, pinned first. Pin and Unpin show at
 * once and run in [ReadOrder.saving], so a read that began before does not move a card back; a
 * failure puts the old order back and [saveFailed] says so.
 */
class LibrariesState(private val client: ApiClient, private val signedOut: () -> Unit) {
    var libraries by mutableStateOf<List<LibrarySummary>>(emptyList())
        private set
    var loaded by mutableStateOf(false)
        private set
    var problem by mutableStateOf<Throwable?>(null)
        private set
    var saveFailed by mutableStateOf(false)
        private set

    private var order: List<String> = emptyList()
    private val pinOrder = ReadOrder()

    suspend fun load() {
        val ticket = pinOrder.begin()
        loading(signedOut, { if (pinOrder.fresh(ticket)) problem = it }) {
            val all = client.librarySummaries()
            if (!pinOrder.fresh(ticket)) return@loading
            order = all.map { it.id }
            libraries = pinnedFirst(all, order)
            problem = null
            loaded = true
        }
    }

    /** Pins or unpins [id] (`PUT /api/me/pinned-libraries`); the home's order wins. */
    suspend fun togglePin(id: String) {
        val before = libraries
        val pins = togglePins(before, id)
        libraries = pinnedFirst(withPins(before, pins), order.ifEmpty { before.map { it.id } })
        saveFailed = false
        loading(signedOut, { libraries = before; saveFailed = true }) {
            val saved = pinOrder.saving { client.setPinnedLibraries(pins) }
            libraries = pinnedFirst(withPins(libraries, saved.libraryIds), order.ifEmpty { before.map { it.id } })
        }
    }

    private fun withPins(list: List<LibrarySummary>, pins: List<String>) = list.map { lib ->
        val at = pins.indexOf(lib.id)
        lib.copy(pinned = at >= 0, pinPosition = at.takeIf { it >= 0 })
    }
}
