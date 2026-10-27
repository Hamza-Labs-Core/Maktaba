package com.hamzalabs.maktaba.tv.data

import com.hamzalabs.maktaba.core.session.ConnectLink
import com.hamzalabs.maktaba.core.session.FoundHome
import com.hamzalabs.maktaba.core.session.Home
import com.hamzalabs.maktaba.core.session.addressCandidates

/**
 * What the TV's home picker lists and tries (Epic 42 story 42.10), on the core's rules: homes
 * are known by their exact address (an announced id is unauthenticated, so a known id at a
 * new address is a new home, signed in afresh), and a typed address is asked whether it is a
 * Maktaba home before it is used.
 */
object HomeChoices {
    /** The homes on this network that are not already among [saved], by exact address. */
    fun nearby(found: List<FoundHome>, saved: List<Home>): List<FoundHome> {
        val known = saved.mapTo(HashSet()) { it.url }
        return found.filter { it.url !in known }
    }

    /**
     * What a person typed in "Enter an address": a `maktaba://connect` link they pasted, or
     * the addresses to try (as typed with its path, then its root). Null: not an address.
     */
    fun typed(text: String): ConnectLink? {
        if (text.isBlank()) return null
        ConnectLink.parse(text)?.let { return it }
        val urls = addressCandidates(text)
        return if (urls.isEmpty()) null else ConnectLink(null, null, urls)
    }

    /** The approve page as read out on screen: its address without the code (the code shows large). */
    fun shownApprovePage(approveUrl: String): String =
        approveUrl.substringBefore('?').substringAfter("://").trimEnd('/')
}
