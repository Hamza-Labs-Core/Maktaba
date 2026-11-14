package com.hamzalabs.maktaba.core.ui

import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import com.hamzalabs.maktaba.core.api.ApiClient
import com.hamzalabs.maktaba.core.api.ApiException
import com.hamzalabs.maktaba.core.api.CloudCode
import com.hamzalabs.maktaba.core.api.CloudCodeLookup
import com.hamzalabs.maktaba.core.api.CloudTvCode
import com.hamzalabs.maktaba.core.api.ErrorKind
import com.hamzalabs.maktaba.core.api.PairingCode
import com.hamzalabs.maktaba.core.api.PairingLookup
import com.hamzalabs.maktaba.core.api.PairingRequest
import com.hamzalabs.maktaba.core.api.approveCloudCode
import com.hamzalabs.maktaba.core.api.cloudCode
import com.hamzalabs.maktaba.core.api.cloudNotLinked
import com.hamzalabs.maktaba.core.api.errorKind
import kotlinx.coroutines.CancellationException
import java.util.Locale

/** Where approving a sign-in stands. */
sealed interface ApproveStep {
    data object Enter : ApproveStep
    data object Looking : ApproveStep

    /** The home's own code and what asks with it (null: the home cannot say). */
    data class Confirm(val code: String, val request: PairingRequest?) : ApproveStep

    /** A Maktaba Cloud code: the TV to confirm (its platform and country) before approving it. */
    data class CloudConfirm(val code: String, val tv: CloudTvCode) : ApproveStep

    /** The home's code approved: [who] is the device's name (null: it gave none). */
    data class Done(val who: String?) : ApproveStep

    /** The cloud code approved: the TV is signing in. */
    data object CloudDone : ApproveStep
}

/** Why a step did not go through, shown under the form. */
sealed interface ApproveProblem {
    /** Not eight letters and digits. */
    data object Invalid : ApproveProblem

    /** A scanned QR that holds no sign-in code. */
    data object NotACode : ApproveProblem

    /** Wrong or expired (404, 410, 422). */
    data object Unknown : ApproveProblem

    /** Already approved (409). */
    data object Already : ApproveProblem

    /** A sign-in through Maktaba Cloud cannot approve (403): approve from home. */
    data object FromCloud : ApproveProblem

    /** A cloud code, and this home has no remote access to reach Maktaba Cloud with. */
    data object NeedsRemote : ApproveProblem

    /** The home could not be reached. */
    data object Unreachable : ApproveProblem

    /** Anything else, by its kind. */
    data class Other(val kind: ErrorKind) : ApproveProblem
}

/**
 * "Approve a TV or device"'s steps, kept out of the screen so they are tested as they are
 * (Epic 42 story 42.6, Epic 58 story 58.03). The home's code first; a code the home does not
 * know that has the cloud's shape is looked up through the home's cloud link, the person
 * confirms the TV (its platform and country), and only then is it approved.
 * [onSignedOut] when the home no longer takes this app's tokens.
 */
class ApproveCode(private val client: ApiClient, private val onSignedOut: () -> Unit = {}) {
    var step: ApproveStep by mutableStateOf(ApproveStep.Enter)
        private set
    var problem: ApproveProblem? by mutableStateOf(null)
    var busy: Boolean by mutableStateOf(false)
        private set

    /** Starts again (the code was changed, Cancel, or Approve another). */
    fun reset() {
        step = ApproveStep.Enter
        problem = null
    }

    /** Looks up what [raw] (typed or scanned) is: the home's code first, then a cloud code. */
    suspend fun lookUp(raw: String) {
        val code = PairingCode.normalize(raw) ?: run { problem = ApproveProblem.Invalid; return }
        problem = null
        step = ApproveStep.Looking
        step = try {
            when (val found = client.pairingRequest(code)) {
                is PairingLookup.Waiting -> ApproveStep.Confirm(code, found.request)
                PairingLookup.Unsupported -> ApproveStep.Confirm(code, null)
                PairingLookup.Unknown -> if (CloudCode.isCloudShaped(code)) lookUpCloud(code) else unknown()
            }
        } catch (e: CancellationException) {
            throw e
        } catch (e: Exception) {
            failed(e)
            ApproveStep.Enter
        }
    }

    private suspend fun lookUpCloud(code: String): ApproveStep = when (val found = client.cloudCode(code)) {
        is CloudCodeLookup.Found -> ApproveStep.CloudConfirm(code, found.tv)
        CloudCodeLookup.NotFound -> unknown()
        CloudCodeLookup.NotLinked -> { problem = ApproveProblem.NeedsRemote; ApproveStep.Enter }
    }

    private fun unknown(): ApproveStep {
        problem = ApproveProblem.Unknown
        return ApproveStep.Enter
    }

    /** Approves what is on screen: the home's code, or the cloud code once confirmed. */
    suspend fun approve() {
        val s = step
        if (s !is ApproveStep.Confirm && s !is ApproveStep.CloudConfirm) return
        busy = true
        problem = null
        try {
            step = when (s) {
                is ApproveStep.Confirm -> {
                    client.approvePairing(s.code)
                    ApproveStep.Done(s.request?.deviceLabel?.takeIf { it.isNotBlank() })
                }
                is ApproveStep.CloudConfirm -> {
                    client.approveCloudCode(s.code)
                    ApproveStep.CloudDone
                }
                else -> s
            }
        } catch (e: CancellationException) {
            throw e
        } catch (e: Exception) {
            failed(e)
        } finally {
            busy = false
        }
    }

    private fun failed(e: Exception) {
        if (e is ApiException.SignedOut) {
            onSignedOut()
            return
        }
        problem = problemOf(e)
    }

    companion object {
        /** What [e] means to the person approving. */
        fun problemOf(e: Exception): ApproveProblem = when (e) {
            is ApiException.Transport -> ApproveProblem.Unreachable
            is ApiException.Status -> when {
                e.cloudNotLinked -> ApproveProblem.NeedsRemote
                e.code == 409 -> ApproveProblem.Already
                e.code == 403 -> ApproveProblem.FromCloud
                e.code == 404 || e.code == 410 || e.code == 422 -> ApproveProblem.Unknown
                else -> ApproveProblem.Other(e.errorKind())
            }
            else -> ApproveProblem.Other(e.errorKind())
        }

        /**
         * The TV's country as [locale] names it ("Jordan", "الأردن"), or null when the cloud
         * did not say (the confirmation then names the platform alone).
         */
        fun countryName(tv: CloudTvCode, locale: Locale): String? {
            val cc = tv.countryCode ?: return null
            val name = runCatching { Locale.Builder().setRegion(cc).build().getDisplayCountry(locale) }.getOrNull()
            return name?.takeIf { it.isNotBlank() && !it.equals(cc, ignoreCase = true) } ?: cc
        }

        /** The platform as a person reads it, by its maker: `tizen` → Samsung; none is null. */
        fun platformName(tv: CloudTvCode): String? = tv.platform.trim().takeIf { it.isNotEmpty() }?.let(::tvPlatformMaker)
    }
}

/**
 * A cloud code's TV platform by its maker (`tizen` → Samsung), as "Samsung TV, via Maktaba Cloud
 * code" reads: product names, the same in every language; an unknown platform capitalised.
 */
fun tvPlatformMaker(platform: String): String = when (platform.trim().lowercase(Locale.ROOT)) {
    "tizen" -> "Samsung"
    "webos" -> "LG"
    "roku" -> "Roku"
    "firetv" -> "Fire"
    "androidtv" -> "Android"
    "tvos" -> "Apple"
    "cast" -> "Google"
    "vidaa" -> "Hisense"
    else -> platform.trim().replaceFirstChar { it.titlecase(Locale.ROOT) }
}
