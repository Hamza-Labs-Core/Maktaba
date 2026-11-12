package com.hamzalabs.maktaba.tv

import com.hamzalabs.maktaba.tv.ui.screens.LiveFailures
import com.hamzalabs.maktaba.tv.ui.screens.LiveFailures.Step
import org.junit.Assert.assertEquals
import org.junit.Test

/**
 * Epic 57 story 57.10 on the TV: a limited account's live stream that keeps failing while the
 * home says it plays (the encoder is down) asks the home once more and then shows the error; it
 * is not tuned again for ever on a black screen.
 */
class LiveFailuresTest {
    private var now = 1_000_000L

    /** The player fails [times] times, 2 s apart; a stream given again starts its warm-up over. */
    private fun LiveFailures.fail(times: Int, gated: Boolean = true): List<Step> = (1..times).map {
        now += LiveFailures.WARM_UP_DELAY_MS
        next(gated).also { if (it == Step.AskHome) started() }
    }

    private val warmUp = List(LiveFailures.WARM_UP_RETRIES) { Step.WarmUp }

    @Test
    fun aLimitedAccountsBrokenStreamAsksTheHomeOnceThenShowsTheError() {
        val f = LiveFailures(limited = { true }, nowMs = { now })
        f.chosen()
        f.started()
        assertEquals(warmUp + Step.AskHome + warmUp + Step.ShowError + Step.ShowError, f.fail(2 * LiveFailures.WARM_UP_RETRIES + 3))
    }

    @Test
    fun tryAgainAsksTheHomeAgain() {
        val f = LiveFailures(limited = { true }, nowMs = { now })
        f.chosen()
        f.started()
        f.fail(2 * LiveFailures.WARM_UP_RETRIES + 2)
        // the person presses Try again
        f.chosen()
        f.started()
        assertEquals(warmUp + Step.AskHome, f.fail(LiveFailures.WARM_UP_RETRIES + 1))
    }

    @Test
    fun aStreamThatPlayedForLongAsksTheHomeAgainWhenItStops() {
        val f = LiveFailures(limited = { true }, nowMs = { now })
        f.chosen()
        f.started()
        assertEquals(Step.AskHome, f.fail(LiveFailures.WARM_UP_RETRIES + 1).last())
        // it played for an hour, then stopped: the programme may have changed
        now += 3_600_000
        assertEquals(Step.AskHome, f.fail(LiveFailures.WARM_UP_RETRIES + 1).last())
    }

    @Test
    fun anAccountWithNoLimitAndARemoteSessionShowTheError() {
        val free = LiveFailures(limited = { false }, nowMs = { now })
        free.started()
        assertEquals(warmUp + Step.ShowError, free.fail(LiveFailures.WARM_UP_RETRIES + 1))
        val relay = LiveFailures(limited = { true }, nowMs = { now })
        relay.started()
        assertEquals(warmUp + Step.ShowError, relay.fail(LiveFailures.WARM_UP_RETRIES + 1, gated = false))
    }
}
