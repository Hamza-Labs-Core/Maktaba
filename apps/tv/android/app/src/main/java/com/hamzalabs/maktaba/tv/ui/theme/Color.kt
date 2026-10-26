package com.hamzalabs.maktaba.tv.ui.theme

import androidx.compose.ui.graphics.Color

/**
 * The "Islamic library" palette — "Ink & Brass", the dark theme. This is
 * the single source of colour for the app: [MaktabaTvTheme] maps it onto
 * the TV Material colour scheme, and components that need a token the
 * scheme has no slot for (panel, faint text, live green) read it from
 * here. `res/values/colors.xml` mirrors the background the window
 * needs before Compose draws — keep the two in sync.
 *
 * Android TV is dark-only: the app has no light mode, and a dark room is
 * where a TV is watched. The light "gold on white" variant lives in the
 * web app and on tvOS (which follows the system appearance).
 */
object MaktabaPalette {
    /** App background — deep ink. */
    val Background = Color(0xFF0B111C)

    /** Navigation bar / sidebar. */
    val Nav = Color(0xFF0F1726)

    /** Cards, fields, panels. */
    val Surface = Color(0xFF131D2E)

    /** Raised or active surface (selected chip, inactive tab pill). */
    val Raised = Color(0xFF1B2638)

    /** Hairline borders. */
    val Border = Color(0xFF22304A)

    /** Primary text. */
    val Text = Color(0xFFE8ECF3)

    /** Secondary text. */
    val Muted = Color(0xFFA9B3C4)

    /** Tertiary text (captions, counts). */
    val Faint = Color(0xFF7F8BA0)

    /** Brass/gold accent — focus rings, fills, the ۞ marker. */
    val Gold = Color(0xFFE3B252)

    /** Lighter gold for hover/pressed. */
    val GoldLight = Color(0xFFF2CB7A)

    /** Text on a gold fill. */
    val OnGold = Color(0xFF1A1408)

    /** Live / OK status. */
    val Live = Color(0xFF4CC38A)

    /**
     * Reading panel over the library art: background at 78% opacity.
     * Compose can't blur what is *behind* a view (Modifier.blur blurs the
     * view's own content, and only on API 31+), so the panel relies on
     * opacity alone — enough to keep text off the busy bookshelves.
     */
    val Panel = Background.copy(alpha = 0.78f)

    /** Translucent nav band drawn over the art on Home. */
    val NavTranslucent = Nav.copy(alpha = 0.88f)
}
