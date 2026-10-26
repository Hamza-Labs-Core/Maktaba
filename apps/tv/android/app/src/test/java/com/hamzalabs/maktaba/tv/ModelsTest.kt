package com.hamzalabs.maktaba.tv

import com.hamzalabs.maktaba.tv.data.resumeAt
import com.hamzalabs.maktaba.tv.ui.components.qrModules
import com.hamzalabs.maktaba.tv.ui.screens.clock
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

class ModelsTest {
    @Test
    fun resumeOnlyWhereThereIsSomethingLeftToWatch() {
        assertEquals(125.0, resumeAt(125.0, 600.0, completed = false)!!, 0.0)
        assertNull("not started", resumeAt(null, 600.0, false))
        assertNull("the first seconds", resumeAt(4.0, 600.0, false))
        assertNull("finished", resumeAt(300.0, 600.0, true))
        assertNull("the last seconds", resumeAt(590.0, 600.0, false))
        assertEquals("length unknown", 300.0, resumeAt(300.0, null, false)!!, 0.0)
    }

    @Test
    fun lengthsReadLikeThePlayer() {
        assertEquals("4:05", clock(245.0))
        assertEquals("1:02:03", clock(3723.0))
    }

    @Test
    fun theApprovePageFitsInAQr() {
        val m = qrModules("http://192.168.50.177:8088/pair?code=ABCD-1234")
        assertNotNull(m)
        assertTrue(m!!.size in 21..41)
        assertEquals(m.size, m[0].size)
        // the finder pattern's corner module is dark
        assertTrue(m[0][0])
    }
}
