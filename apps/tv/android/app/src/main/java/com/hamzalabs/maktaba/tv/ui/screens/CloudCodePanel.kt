package com.hamzalabs.maktaba.tv.ui.screens

import android.os.Build
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.runtime.Composable
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableLongStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalLayoutDirection
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.unit.LayoutDirection
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.tv.material3.MaterialTheme
import androidx.tv.material3.Text
import com.hamzalabs.maktaba.core.diagnostics.AppLog
import com.hamzalabs.maktaba.core.session.AppSession
import com.hamzalabs.maktaba.core.session.HomeProbe
import com.hamzalabs.maktaba.core.session.TvCodeState
import com.hamzalabs.maktaba.core.session.TvCodes
import com.hamzalabs.maktaba.core.session.waitForTvApproval
import com.hamzalabs.maktaba.tv.BuildConfig
import com.hamzalabs.maktaba.tv.R
import com.hamzalabs.maktaba.tv.data.HomeChoices
import com.hamzalabs.maktaba.tv.ui.components.MaktabaButton
import com.hamzalabs.maktaba.tv.ui.components.QrCode
import com.hamzalabs.maktaba.tv.ui.components.ReadingPanel
import com.hamzalabs.maktaba.tv.ui.components.SectionTitle
import com.hamzalabs.maktaba.tv.ui.theme.MaktabaPalette
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.delay
import okhttp3.HttpUrl.Companion.toHttpUrlOrNull
import com.hamzalabs.maktaba.core.R as CoreR

/** What the cloud code panel shows. */
sealed interface CloudCodeUi {
    /** Asking Maktaba Cloud for a code. */
    data object Asking : CloudCodeUi

    /** [code] on screen (`ABCD-2345`), its QR opening [verifyUrl], running out in [expiresInSec]. */
    data class Showing(val code: String, val verifyUrl: String, val expiresInSec: Long) : CloudCodeUi

    /** The cloud gave no code: the other ways stay. */
    data object Down : CloudCodeUi

    /** Approved: opening [home]. */
    data class Opening(val home: String) : CloudCodeUi

    /** Approved, but [home] did not answer from this TV, or refused the sign-in. */
    data class Unreachable(val home: String) : CloudCodeUi
}

/** What this TV is called in the home's list of sign-ins: "Android TV (model)". */
@Composable
internal fun tvDeviceLabel(): String = stringResource(R.string.tv_label).let { base ->
    val model = Build.MODEL?.takeIf { it.isNotBlank() }
    if (model != null) "$base ($model)" else base
}

/**
 * The cloud code (Epic 58 story 58.03's TV side, 58.05): the first way in on the home picker.
 * Asks Maktaba Cloud for a code, shows it until a person signed in to a home approves it (from
 * any network), then signs this TV in at that home ([com.hamzalabs.maktaba.core.session.AppSession.signInWithTvApproval]).
 * Leaving the picker stops it; the session finishes a sign-in already under way.
 */
@Composable
fun CloudCodePanel(session: AppSession, modifier: Modifier = Modifier, probe: HomeProbe = remember { HomeProbe() }) {
    val label = tvDeviceLabel()
    var ui by remember { mutableStateOf<CloudCodeUi>(CloudCodeUi.Asking) }
    var expiresAt by remember { mutableLongStateOf(0L) }
    var shown by remember { mutableStateOf<Pair<String, String>?>(null) }
    // Try again: a new code, from the start
    var run by remember { mutableIntStateOf(0) }
    val codes = remember(session.cloudApi) { session.cloudApi.toHttpUrlOrNull()?.let { TvCodes(it) } }

    LaunchedEffect(run, codes) {
        if (codes == null) {
            ui = CloudCodeUi.Down
            return@LaunchedEffect
        }
        val approval = waitForTvApproval(codes, BuildConfig.TV_PLATFORM, BuildConfig.VERSION_NAME, onState = { s ->
            when (s) {
                TvCodeState.Asking -> if (shown == null) ui = CloudCodeUi.Asking
                TvCodeState.Down -> { shown = null; ui = CloudCodeUi.Down }
                is TvCodeState.Showing -> {
                    shown = s.code.code to s.code.verifyUrl
                    expiresAt = s.expiresAtMs
                    ui = CloudCodeUi.Showing(s.code.code, s.code.verifyUrl, ((s.expiresAtMs - System.currentTimeMillis()) / 1000).coerceAtLeast(0))
                }
            }
        })
        val name = approval.homeName.ifBlank { approval.homeId }
        ui = CloudCodeUi.Opening(name)
        try {
            session.signInWithTvApproval(approval, label, probe)
        } catch (e: CancellationException) {
            throw e
        } catch (e: Exception) {
            AppLog.failure("tv cloud code", e)
            ui = CloudCodeUi.Unreachable(name)
        }
    }

    // the time left, once a second while a code shows
    LaunchedEffect(shown) {
        while (shown != null) {
            val current = ui
            if (current is CloudCodeUi.Showing) {
                ui = current.copy(expiresInSec = ((expiresAt - System.currentTimeMillis()) / 1000).coerceAtLeast(0))
            }
            delay(1_000)
        }
    }

    CloudCodeContent(ui, onTryAgain = { shown = null; run++ }, modifier = modifier)
}

/**
 * The panel without the cloud: what [CloudCodePanel] shows for [ui]. Where to approve the code
 * (the phones' button, by the phone apps' own string), that it works from any network, the code
 * large with its QR, how long it lasts, and what the TV is doing.
 */
@Composable
internal fun CloudCodeContent(ui: CloudCodeUi, onTryAgain: () -> Unit, modifier: Modifier = Modifier) {
    ReadingPanel(modifier.testTag("cloud-code-panel"), contentPadding = 20.dp) {
        SectionTitle(stringResource(R.string.cloud_code_title))
        Text(
            stringResource(R.string.cloud_code_where, stringResource(CoreR.string.approve_tv)),
            style = MaterialTheme.typography.bodyMedium,
            color = MaktabaPalette.Text,
            modifier = Modifier.padding(top = 8.dp, bottom = 4.dp).testTag("cloud-where"),
        )
        when (ui) {
            CloudCodeUi.Asking -> Status(stringResource(R.string.cloud_code_asking), MaktabaPalette.Muted)
            CloudCodeUi.Down -> Status(stringResource(R.string.cloud_code_down), MaktabaPalette.Gold)
            is CloudCodeUi.Opening -> Status(stringResource(R.string.cloud_code_opening, ui.home), MaktabaPalette.Text)
            is CloudCodeUi.Unreachable -> {
                Status(stringResource(R.string.cloud_code_unreachable, ui.home), MaktabaPalette.Gold)
                Box(Modifier.padding(top = 12.dp)) {
                    MaktabaButton(onClick = onTryAgain, modifier = Modifier.testTag("cloud-try-again")) { Text(stringResource(R.string.try_again)) }
                }
            }
            is CloudCodeUi.Showing -> Row(horizontalArrangement = Arrangement.spacedBy(24.dp), verticalAlignment = Alignment.CenterVertically) {
                Column(Modifier.weight(1f), verticalArrangement = Arrangement.spacedBy(8.dp)) {
                    // the code reads left to right in Arabic too
                    CompositionLocalProvider(LocalLayoutDirection provides LayoutDirection.Ltr) {
                        Text(
                            ui.code,
                            style = MaterialTheme.typography.displaySmall.copy(fontFamily = FontFamily.Monospace, letterSpacing = 3.sp),
                            color = MaktabaPalette.Gold,
                            maxLines = 1,
                            modifier = Modifier.testTag("cloud-code")
                                .semantics { contentDescription = ui.code.toCharArray().joinToString(" ") },
                        )
                    }
                    Text(
                        stringResource(R.string.signin_code_expires, shownTimeLeft(ui.expiresInSec)),
                        style = MaterialTheme.typography.titleMedium,
                        color = MaktabaPalette.Muted,
                        modifier = Modifier.testTag("cloud-expires"),
                    )
                    Text(
                        stringResource(R.string.signin_code_waiting),
                        style = MaterialTheme.typography.titleMedium,
                        color = MaktabaPalette.Text,
                        modifier = Modifier.testTag("cloud-status"),
                    )
                }
                if (ui.verifyUrl.isNotBlank()) {
                    Column(Modifier.width(192.dp), horizontalAlignment = Alignment.CenterHorizontally, verticalArrangement = Arrangement.spacedBy(8.dp)) {
                        Box(Modifier.testTag("cloud-qr")) { QrCode(ui.verifyUrl, size = 136.dp) }
                        Text(
                            stringResource(R.string.signin_code_computer, "⁦" + HomeChoices.shownApprovePage(ui.verifyUrl) + "⁩"),
                            style = MaterialTheme.typography.bodySmall,
                            color = MaktabaPalette.Muted,
                            textAlign = TextAlign.Center,
                            modifier = Modifier.testTag("cloud-computer"),
                        )
                    }
                }
            }
        }
    }
}

@Composable
private fun Status(text: String, color: androidx.compose.ui.graphics.Color) {
    Text(text, style = MaterialTheme.typography.titleMedium, color = color, modifier = Modifier.padding(top = 8.dp).testTag("cloud-status"))
}
