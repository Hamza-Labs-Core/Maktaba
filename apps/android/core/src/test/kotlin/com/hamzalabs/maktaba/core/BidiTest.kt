package com.hamzalabs.maktaba.core

import com.hamzalabs.maktaba.core.ui.isolated
import org.junit.Assert.assertEquals
import org.junit.Test
import java.text.Bidi

class BidiTest {
    /** The level of each character of [line] in an Arabic (right-to-left) paragraph: odd is right to left. */
    private fun level(line: String, c: Char): Int =
        Bidi(line, Bidi.DIRECTION_RIGHT_TO_LEFT).getLevelAt(line.lastIndexOf(c))

    @Test
    fun anEnglishLineKeepsItsFullStopInTheArabicApp() {
        val line = "The sky crane lowers Curiosity gently onto Mars."
        assertEquals("alone, its full stop goes to the line's other side", 1, level(line, '.'))
        assertEquals("set apart, it stays with its line", 0, level(isolated(line), '.') % 2)
    }

    @Test
    fun onlyTheMarksAreAdded() {
        assertEquals("⁨a b⁩", isolated("a b"))
        assertEquals("", isolated(""))
    }
}
