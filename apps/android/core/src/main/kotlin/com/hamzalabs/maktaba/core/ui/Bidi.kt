package com.hamzalabs.maktaba.core.ui

/** Unicode's first-strong isolate and the mark that ends it. */
private const val FSI = '⁨'
private const val PDI_MARK = '⁩'

/**
 * [text] (a line of a transcript, a title) set apart in its own direction: an English line in the
 * Arabic app keeps its full stop at its end, while it still sits where the app's lines start.
 */
fun isolated(text: String): String = if (text.isEmpty()) text else "$FSI$text$PDI_MARK"
