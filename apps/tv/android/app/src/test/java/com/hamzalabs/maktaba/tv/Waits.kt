package com.hamzalabs.maktaba.tv

/**
 * The net under the tests' waits for a state (an answer read, a screen shown): they return as
 * soon as the state holds, and this is far longer than a slow runner needs, so only a real
 * stall meets it.
 */
const val STALL_MS = 60_000L

/**
 * The calls to the home that go out on [http], counted on its OkHttp dispatcher, so a test can
 * tell when none is on its way. By default [http] is the app's own client settings on a
 * dispatcher of its own: a client made with it is counted alone. It is idle ([idling]) when no
 * call is queued or running. OkHttp counts a call as running until its callback returns, and
 * the client resumes its caller inside that callback, so once this is idle the app's next step
 * is already queued on the main thread and `waitForIdle()` runs it: what that step sends is
 * counted in turn. A test registers [idling] and then reads a count of the home's calls only
 * after `waitForIdle()`: nothing is on its way that could still change it.
 */
class HomeCalls(
    val http: okhttp3.OkHttpClient = com.hamzalabs.maktaba.core.api.ApiClient.defaultHttp.newBuilder().dispatcher(okhttp3.Dispatcher()).build(),
) {
    val running: Int get() = http.dispatcher.runningCallsCount()
    val queued: Int get() = http.dispatcher.queuedCallsCount()

    val idling = object : androidx.compose.ui.test.IdlingResource {
        override val isIdleNow: Boolean get() = running == 0 && queued == 0
    }

    companion object {
        /**
         * Every call the whole signed-in app sends: its parts (the TV's own reads, the queue,
         * Live TV, the status strip) each go out on the app's one default client, so that
         * client's dispatcher is the one counted.
         */
        fun ofTheWholeApp() = HomeCalls(com.hamzalabs.maktaba.core.api.ApiClient.defaultHttp)
    }
}

/**
 * For a test that drives Robolectric's main looper itself: lets everything on its way land.
 * Returns once no call to the home is queued or running but the [held] ones the test holds on
 * the home, [background] has no work alive but [heldJobs], the home was asked nothing new
 * ([asked] counts what it was asked), and the main looper has run what those left it. OkHttp
 * counts a call as running until its callback returns, and the client resumes its caller inside
 * that callback, so a call or coroutine that ends has already queued its next step on the main
 * looper. It waits for that state, never for a length of time: [STALL_MS] is the net under a
 * stall.
 */
fun settleLooper(calls: HomeCalls, background: kotlinx.coroutines.CoroutineScope, asked: () -> Any, held: Int = 0, heldJobs: Int = 0) {
    fun quiet() = calls.running == held && calls.queued == 0 &&
        background.coroutineContext[kotlinx.coroutines.Job]!!.children.count() == heldJobs
    val until = System.nanoTime() + java.util.concurrent.TimeUnit.MILLISECONDS.toNanos(STALL_MS)
    while (true) {
        val seen = asked()
        val before = quiet()
        org.robolectric.Shadows.shadowOf(android.os.Looper.getMainLooper()).idle()
        if (before && quiet() && seen == asked()) return
        check(System.nanoTime() < until) { "the home's answers never settled; it was asked ${asked()}" }
        // nothing due: the answer is still on another thread, which is given the processor
        java.util.concurrent.locks.LockSupport.parkNanos(500_000)
    }
}
