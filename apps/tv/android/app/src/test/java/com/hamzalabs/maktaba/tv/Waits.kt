package com.hamzalabs.maktaba.tv

/**
 * The net under the tests' waits for a state (an answer read, a screen shown): they return as
 * soon as the state holds, and this is far longer than a slow runner needs, so only a real
 * stall meets it.
 */
const val STALL_MS = 60_000L
