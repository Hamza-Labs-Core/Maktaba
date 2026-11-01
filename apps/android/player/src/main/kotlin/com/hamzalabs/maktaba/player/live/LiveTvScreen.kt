package com.hamzalabs.maktaba.player.live

import androidx.compose.foundation.layout.BoxWithConstraints
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.padding
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.PrimaryTabRow
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Tab
import androidx.compose.material3.Text
import androidx.compose.material3.TopAppBar
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableLongStateOf
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.remember
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.foundation.layout.Box
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.unit.dp
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.compose.LocalLifecycleOwner
import androidx.lifecycle.repeatOnLifecycle
import com.hamzalabs.maktaba.core.api.Failure
import com.hamzalabs.maktaba.core.ui.ErrorState
import com.hamzalabs.maktaba.core.ui.Message
import com.hamzalabs.maktaba.player.R
import com.hamzalabs.maktaba.player.shell.LocalPlayerHome
import com.hamzalabs.maktaba.player.shell.PlayerNavigator
import com.hamzalabs.maktaba.player.shell.Route
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch

/** From this width the guide asks for six hours and Now is a grid (FR-21, FR-22). */
internal val WideFrom = 600.dp

/**
 * Live TV (FR-21, FR-22): **Now**, every channel with what is on, its progress and what is next
 * (a list on a phone, a grid on a tablet); and the **Guide**, channels × time. Tapping a channel,
 * or Watch on what is on now, opens its live player. Now refreshes once a minute while shown and
 * the app is in front (NFR-16), never in the background.
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun LiveTvScreen(nav: PlayerNavigator) {
    val live = LocalPlayerHome.current.liveTv()
    val env = LocalLiveEnv.current
    var tab by rememberSaveable { mutableIntStateOf(0) }
    val scope = rememberCoroutineScope()
    // the time the screen shows, the home's: progress moves with it
    var nowMs by remember { mutableLongStateOf(live.serverNow(env.nowMs())) }

    val lifecycle = LocalLifecycleOwner.current.lifecycle
    LaunchedEffect(live, lifecycle) {
        lifecycle.repeatOnLifecycle(Lifecycle.State.STARTED) {
            launch {
                while (true) {
                    live.loadNow(env.nowMs())
                    nowMs = live.serverNow(env.nowMs())
                    delay(env.refreshMs)
                }
            }
            while (true) {
                delay(env.tickMs)
                nowMs = live.serverNow(env.nowMs())
            }
        }
    }

    Scaffold(
        modifier = Modifier.testTag("live-tv"),
        topBar = { TopAppBar(title = { Text(stringResource(R.string.section_live_tv)) }) },
    ) { padding ->
        BoxWithConstraints(Modifier.padding(padding).fillMaxSize()) {
            val wide = maxWidth >= WideFrom
            val now = live.now
            val problem = live.nowProblem
            when {
                now == null && problem != null -> ErrorState(
                    Failure.of(problem),
                    title = stringResource(R.string.live_cant_load),
                    onRetry = { scope.launch { live.loadNow(env.nowMs()) } },
                )
                now == null -> Box(Modifier.fillMaxSize(), contentAlignment = Alignment.Center) { CircularProgressIndicator() }
                now.channels.isEmpty() -> Message(
                    stringResource(R.string.live_no_channels),
                    detail = stringResource(R.string.live_no_channels_detail),
                )
                else -> Column(Modifier.fillMaxSize()) {
                    PrimaryTabRow(selectedTabIndex = tab) {
                        Tab(tab == 0, onClick = { tab = 0 }, text = { Text(stringResource(R.string.live_now)) }, modifier = Modifier.testTag("tab-now"))
                        Tab(tab == 1, onClick = { tab = 1 }, text = { Text(stringResource(R.string.live_guide)) }, modifier = Modifier.testTag("tab-guide"))
                    }
                    val watch = { id: String -> nav.open(Route.Channel(id)) }
                    if (tab == 0) {
                        NowTab(live, now.channels, nowMs, wide, onOpen = watch)
                    } else {
                        GuideTab(live, nowMs, wide, onWatch = watch)
                    }
                }
            }
        }
    }
}
