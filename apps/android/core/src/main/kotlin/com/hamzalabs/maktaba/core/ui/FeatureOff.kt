package com.hamzalabs.maktaba.core.ui

import android.content.ActivityNotFoundException
import android.content.Context
import android.content.Intent
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.padding
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.unit.dp
import androidx.core.net.toUri
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.compose.LocalLifecycleOwner
import androidx.lifecycle.repeatOnLifecycle
import com.hamzalabs.maktaba.core.R
import com.hamzalabs.maktaba.core.api.componentsUrl
import com.hamzalabs.maktaba.core.session.AppSession

/**
 * Keeps [session]'s capabilities fresh while the app is started (story 51.11): asked each time
 * it comes to the front, then every five minutes while it stays there; never in the background.
 */
@Composable
fun FollowCapabilities(session: AppSession) {
    val lifecycle = LocalLifecycleOwner.current.lifecycle
    LaunchedEffect(session, lifecycle) {
        lifecycle.repeatOnLifecycle(Lifecycle.State.STARTED) { session.followCapabilities() }
    }
}

/** "Live TV is off on this home.", with the feature's own name ([feature]: a capabilities id). */
@Composable
fun featureOffSentence(feature: String): String =
    stringResource(R.string.feature_off_admin, featureName(feature)?.let { stringResource(it) } ?: feature)

/**
 * What an admin reads where a feature that is off would be (story 51.11): "[feature] is off on
 * this home." and a button that opens the home's Components page in the browser, where it is
 * turned on. A viewer never sees it: the feature is hidden from them.
 */
@Composable
fun FeatureOffNote(feature: String, homeUrl: String, modifier: Modifier = Modifier, centered: Boolean = false) {
    val context = LocalContext.current
    Column(
        modifier.padding(16.dp).testTag("feature-off-$feature"),
        verticalArrangement = Arrangement.spacedBy(12.dp),
        horizontalAlignment = if (centered) Alignment.CenterHorizontally else Alignment.Start,
    ) {
        Text(
            featureOffSentence(feature),
            style = MaterialTheme.typography.bodyLarge,
            textAlign = if (centered) TextAlign.Center else TextAlign.Start,
        )
        OutlinedButton(onClick = { openComponents(context, homeUrl) }, modifier = Modifier.testTag("feature-turn-on")) {
            Text(stringResource(R.string.feature_turn_on))
        }
    }
}

/** Opens the home's Components page in the browser (nothing when no app opens it). */
fun openComponents(context: Context, homeUrl: String) {
    try {
        context.startActivity(Intent(Intent.ACTION_VIEW, componentsUrl(homeUrl).toUri()).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK))
    } catch (_: ActivityNotFoundException) {
        // no browser: nothing to do
    }
}
