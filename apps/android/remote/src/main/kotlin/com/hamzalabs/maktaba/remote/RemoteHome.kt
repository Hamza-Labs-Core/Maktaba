package com.hamzalabs.maktaba.remote

import androidx.activity.compose.BackHandler
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxHeight
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.LazyListScope
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material.icons.outlined.Dns
import androidx.compose.material.icons.outlined.ReportProblem
import androidx.compose.material.icons.outlined.Sync
import androidx.compose.material.icons.outlined.VideoLibrary
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Text
import androidx.compose.material3.TopAppBar
import androidx.compose.material3.VerticalDivider
import androidx.compose.material3.adaptive.navigationsuite.NavigationSuiteScaffold
import androidx.compose.material3.pulltorefresh.PullToRefreshBox
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.unit.dp
import com.hamzalabs.maktaba.core.api.ApiClient
import com.hamzalabs.maktaba.core.api.ApiException
import com.hamzalabs.maktaba.core.api.User
import com.hamzalabs.maktaba.core.layout.HingeSplit
import com.hamzalabs.maktaba.core.layout.ListPaneWidth
import com.hamzalabs.maktaba.core.layout.Panes
import com.hamzalabs.maktaba.core.layout.currentPanes
import com.hamzalabs.maktaba.core.session.AppSession
import com.hamzalabs.maktaba.core.ui.AccountMenu
import com.hamzalabs.maktaba.core.ui.Message
import com.hamzalabs.maktaba.core.ui.ThemeStore
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.launch

/** The Remote's places: what needs attention first, as on the web's queue page. */
enum class Place(val label: String, val icon: ImageVector) {
    Attention("Needs attention", Icons.Outlined.ReportProblem),
    Queue("Queue", Icons.Outlined.Sync),
    Libraries("Libraries", Icons.Outlined.VideoLibrary),
    Machines("Machines", Icons.Outlined.Dns),
}

/** What every Remote screen needs: the client, and how to report a failure or a sign-out. */
class Remote(val client: ApiClient, private val session: AppSession) {
    var problem by mutableStateOf<String?>(null)

    /** Runs [block]; a sign-out goes back to signing in, anything else shows as the problem. */
    suspend fun <T> call(block: suspend ApiClient.() -> T): T? = try {
        client.block().also { problem = null }
    } catch (e: CancellationException) {
        throw e
    } catch (e: ApiException.SignedOut) {
        session.signedOut()
        null
    } catch (e: ApiException.Status) {
        problem = if (e.code == 403) "The home refused: only its admins can do this." else e.message
        null
    } catch (e: Exception) {
        problem = e.message
        null
    }
}

/**
 * The Remote's frame: a bottom bar on a phone, a rail on a tablet or an open foldable (Material's
 * navigation suite picks), and the chosen place.
 */
@Composable
fun RemoteHome(session: AppSession, user: User, client: ApiClient, themes: ThemeStore) {
    var place by rememberSaveable { mutableStateOf(Place.Attention) }
    val remote = remember(client) { Remote(client, session) }
    val account: @Composable () -> Unit = { AccountMenu(session, user, themes) }
    NavigationSuiteScaffold(
        navigationSuiteItems = {
            Place.entries.forEach { p ->
                item(
                    selected = p == place,
                    onClick = { place = p; remote.problem = null },
                    icon = { Icon(p.icon, null) },
                    label = { Text(p.label) },
                )
            }
        },
    ) {
        when (place) {
            Place.Attention -> AttentionScreen(remote, account)
            Place.Queue -> QueueScreen(remote, account)
            Place.Libraries -> LibrariesScreen(remote, account)
            Place.Machines -> MachinesScreen(remote, account)
        }
    }
}

/**
 * A screen of rows and the chosen row's detail, laid out by the window: one pane (the detail
 * over the list, back to return), the list beside the detail, or split at a half-open hinge
 * (the detail on the first half, the list on the second, where it is touched).
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun AdminScreen(
    title: String,
    remote: Remote,
    account: @Composable () -> Unit,
    refreshing: Boolean,
    onRefresh: suspend () -> Unit,
    actions: @Composable () -> Unit = {},
    chosenTitle: String? = null,
    onUnchoose: () -> Unit = {},
    detail: (@Composable () -> Unit)? = null,
    rows: LazyListScope.() -> Unit,
) {
    val panes = currentPanes()
    val scope = rememberCoroutineScope()
    val list: @Composable (Modifier) -> Unit = { modifier ->
        Scaffold(
            modifier = modifier,
            topBar = { TopAppBar(title = { Text(title) }, actions = { actions(); account() }) },
        ) { padding ->
            PullToRefreshBox(
                isRefreshing = refreshing,
                onRefresh = { scope.launch { onRefresh() } },
                modifier = Modifier.padding(padding).fillMaxSize(),
            ) {
                LazyColumn(Modifier.fillMaxSize()) {
                    remote.problem?.let { p ->
                        item {
                            Text(
                                p,
                                color = MaterialTheme.colorScheme.error,
                                modifier = Modifier.fillMaxWidth().padding(16.dp),
                            )
                        }
                    }
                    rows()
                }
            }
        }
    }
    val page: @Composable (back: Boolean) -> Unit = { back ->
        Scaffold(
            topBar = {
                TopAppBar(
                    title = { Text(chosenTitle ?: "") },
                    navigationIcon = {
                        if (back) IconButton(onClick = onUnchoose) { Icon(Icons.AutoMirrored.Filled.ArrowBack, "Back") }
                    },
                )
            },
        ) { padding ->
            Box(Modifier.padding(padding).fillMaxSize()) {
                if (detail != null) detail() else Message("Choose one to see it here")
            }
        }
    }
    when (panes) {
        is Panes.Tabletop, is Panes.Book -> HingeSplit(panes, first = { page(detail != null) }, second = { list(Modifier.fillMaxSize()) })
        Panes.ListDetail -> Row(Modifier.fillMaxSize()) {
            list(Modifier.width(ListPaneWidth + 40.dp).fillMaxHeight())
            VerticalDivider(color = MaterialTheme.colorScheme.outlineVariant)
            Column(Modifier.weight(1f)) { page(false) }
        }
        Panes.One -> if (detail != null) {
            BackHandler(onBack = onUnchoose)
            page(true)
        } else {
            list(Modifier.fillMaxSize())
        }
    }
}
