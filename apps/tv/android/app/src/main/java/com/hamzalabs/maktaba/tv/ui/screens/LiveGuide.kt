package com.hamzalabs.maktaba.tv.ui.screens

import com.hamzalabs.maktaba.core.api.GuideRow
import com.hamzalabs.maktaba.core.api.LiveChannel
import com.hamzalabs.maktaba.core.api.Programme

// The Live TV guide's model, without Compose (Epic 48 story 48.35): the cells a row is cut
// into and how the remote's D-pad moves between them. The screen (LiveScreen) draws it and
// feeds it the keys; LiveGuideTest walks it on the JVM.

/** One focusable block of a channel's row over [startMs, stopMs); [programme] null: nothing scheduled. */
data class GuideCell(val startMs: Long, val stopMs: Long, val programme: Programme?)

/** A channel's row of the guide, cut into cells that cover the whole window without gaps. */
data class GuideChannel(val channel: LiveChannel, val cells: List<GuideCell>)

/** The guide's span on screen: two hours, as Epic 46's big-screen guide. */
const val GUIDE_SPAN_MS = 2 * 60 * 60_000L

/** The guide scrolls in half hours, and its time labels sit on them. */
const val HALF_HOUR_MS = 30 * 60_000L

/** A gap shorter than this (a bumper, a station id) is folded into the programme before it. */
private const val SMALL_GAP_MS = 5 * 60_000L

/** [ms] rounded down to its half hour. */
fun floorHalfHour(ms: Long): Long = ms - Math.floorMod(ms, HALF_HOUR_MS)

/**
 * The guide's rows over [fromMs, toMs): each channel's programmes (filler left out) clipped to
 * the window, a short gap folded into the programme before it, and a longer one a cell of its
 * own, so every moment of every row can take focus. A channel with nothing scheduled is one
 * empty cell (Select on it still tunes the channel). Channels in their numbers' order.
 */
fun guideRows(rows: List<GuideRow>, fromMs: Long, toMs: Long): List<GuideChannel> =
    rows.sortedWith(compareBy({ it.channel.number }, { it.channel.name })).map { row ->
        val cells = mutableListOf<GuideCell>()
        var at = fromMs
        val blocks = row.blocks
            .filter { !it.isFiller && it.startMs != null && it.stopMs != null }
            .sortedBy { it.startMs }
        for (b in blocks) {
            val s = maxOf(b.startMs!!, fromMs)
            val e = minOf(b.stopMs!!, toMs)
            if (e <= s || e <= at) continue
            val start = maxOf(s, at)
            if (start > at) {
                val last = cells.lastOrNull()
                if (start - at < SMALL_GAP_MS && last != null) cells[cells.lastIndex] = last.copy(stopMs = start)
                else cells += GuideCell(at, start, null)
            }
            cells += GuideCell(start, e, b)
            at = e
        }
        if (at < toMs) {
            val last = cells.lastOrNull()
            if (toMs - at < SMALL_GAP_MS && last != null) cells[cells.lastIndex] = last.copy(stopMs = toMs)
            else cells += GuideCell(at, toMs, null)
        }
        GuideChannel(row.channel, cells)
    }

/** A press of the remote's D-pad. */
enum class GuideKey { Up, Down, Left, Right }

/**
 * Where the guide's focus is: [row] and [cell], the time [anchorMs] that ▲ and ▼ keep (as a TV
 * guide does: moving between channels stays on the same moment, not the same cell number), and
 * [viewStartMs], the time at the guide's start edge.
 */
data class GuideFocus(val row: Int, val cell: Int, val anchorMs: Long, val viewStartMs: Long)

/** The guide's focus moves over [rows], which begin at [fromMs]; the view spans [spanMs]. */
class GuideNav(val rows: List<GuideChannel>, val fromMs: Long, val spanMs: Long = GUIDE_SPAN_MS) {
    /** The first focus: the first channel's cell on air at [nowMs], the view at the guide's start. */
    fun start(nowMs: Long, row: Int = 0): GuideFocus? {
        val r = rows.getOrNull(row) ?: rows.firstOrNull() ?: return null
        val at = nowMs.coerceAtLeast(fromMs)
        return GuideFocus(rows.indexOf(r), cellAt(r, at), at, fromMs)
    }

    /**
     * Where [key] moves the focus, or null at the guide's edge: ▲ on the first channel (focus
     * leaves to the tab bar), ▼ on the last, and before the first or after the last cell.
     * ◀ and ▶ follow time as it is drawn: right is later in English, left is later in Arabic
     * ([rtl]), where the timeline runs right to left.
     */
    fun move(f: GuideFocus, key: GuideKey, rtl: Boolean): GuideFocus? = when (key) {
        GuideKey.Up -> toRow(f, f.row - 1)
        GuideKey.Down -> toRow(f, f.row + 1)
        GuideKey.Right -> toCell(f, if (rtl) -1 else 1)
        GuideKey.Left -> toCell(f, if (rtl) 1 else -1)
    }

    /** The focused cell. */
    fun cell(f: GuideFocus): GuideCell? = rows.getOrNull(f.row)?.cells?.getOrNull(f.cell)

    private fun toRow(f: GuideFocus, row: Int): GuideFocus? {
        val r = rows.getOrNull(row) ?: return null
        return f.copy(row = row, cell = cellAt(r, f.anchorMs))
    }

    private fun toCell(f: GuideFocus, step: Int): GuideFocus? {
        val cells = rows[f.row].cells
        val i = f.cell + step
        val c = cells.getOrNull(i) ?: return null
        val view = follow(c, f.viewStartMs)
        return GuideFocus(f.row, i, maxOf(c.startMs, view), view)
    }

    /** The view's start once [c] is focused: unchanged while [c] shows, else [c]'s half hour. */
    private fun follow(c: GuideCell, viewStart: Long): Long {
        if (c.stopMs > viewStart && c.startMs < viewStart + spanMs) return viewStart
        return floorHalfHour(c.startMs).coerceAtLeast(fromMs)
    }

    private fun cellAt(r: GuideChannel, at: Long): Int {
        val i = r.cells.indexOfFirst { at >= it.startMs && at < it.stopMs }
        if (i >= 0) return i
        val before = r.cells.indexOfLast { it.startMs <= at }
        return if (before >= 0) before else 0
    }
}

/** The channel [step] places from [current] in [ids] (▲ and ▼ in the player), wrapping round; null with none. */
fun stepChannel(ids: List<String>, current: String?, step: Int): String? {
    if (ids.isEmpty()) return null
    val i = ids.indexOf(current)
    if (i < 0) return ids.first()
    return ids[Math.floorMod(i + step, ids.size)]
}
