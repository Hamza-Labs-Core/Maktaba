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
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.unit.dp
import com.hamzalabs.maktaba.core.api.ApiClient
import com.hamzalabs.maktaba.core.api.ApiException
import com.hamzalabs.maktaba.core.api.Failure
import com.hamzalabs.maktaba.core.diagnostics.AppLog
import com.hamzalabs.maktaba.core.api.User
import com.hamzalabs.maktaba.core.layout.HingeSplit
import com.hamzalabs.maktaba.core.layout.ListPaneWidth
import com.hamzalabs.maktaba.core.layout.Panes
import com.hamzalabs.maktaba.core.layout.currentPanes
import com.hamzalabs.maktaba.core.session.AppSession
import com.hamzalabs.maktaba.core.ui.AccountMenu
import com.hamzalabs.maktaba.core.ui.Message
import com.hamzalabs.maktaba.core.ui.ThemeStore
import com.hamzalabs.maktaba.core.ui.sentence
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.launch

/** The Remote's places: what needs attention first, as on the web's queue page. */
enum class Place(val label: String, val icon: ImageVector) {
    Attention("Needs attention", Icons.Outlined.ReportProblem),
    Queue("Queue", Icons.Outlined.Sync),
    Libraries("Libraries", Icons.Outlined.VideoLibrary),
    Machines("Machines", Icons.Outlined.Dns),
}

/**
 * What every Remote screen needs: the client, and how to report a failure or a sign-out.
 * A screen's loads and its actions fail apart: the next poll that works clears a load's
 * failure, never an action's (that stays until another action, a refresh or another place).
 */
class Remote(val client: ApiClient, private val session: AppSession) {
    private var loadProblem by mutableStateOf<Failure?>(null)
    private var actionProblems by mutableStateOf<List<ActionProblem>>(emptyList())

    /**
     * What went wrong, to show above the list: the actions' failures (each labelled by what it
     * was), else the last load's. Each says its [Failure.kind]'s sentence, never the home's text.
     */
    val problems: List<ActionProblem>
        get() = actionProblems.ifEmpty { listOfNotNull(loadProblem?.let { ActionProblem(null, it) }) }

    val hasProblem: Boolean get() = problems.isNotEmpty()

    fun clear() {
        loadProblem = null
        actionProblems = emptyList()
    }

    /** Reads the home's state: a failure shows until a read works again. */
    suspend fun <T> load(block: suspend ApiClient.() -> T): T? =
        attempt("remote load", block).also { loadProblem = if (it != null) null else lastError }

    /** Changes the home: each failure is kept and shown, labelled by [what] when given. */
    suspend fun act(what: String? = null, block: suspend ApiClient.() -> Unit): Boolean {
        if (attempt("remote action", block) != null) return true
        val why = lastError ?: return false
        actionProblems = actionProblems + ActionProblem(what, why)
        return false
    }

    /** A new action starts with a clean slate. */
    fun startAction() {
        actionProblems = emptyList()
    }

    private var lastError: Failure? = null

    /** Runs [block]; a sign-out goes back to signing in, anything else is logged and the last error. */
    private suspend fun <T> attempt(where: String, block: suspend ApiClient.() -> T): T? = try {
        client.block().also { lastError = null }
    } catch (e: CancellationException) {
        throw e
    } catch (e: ApiException.SignedOut) {
        session.signedOut()
        lastError = null
        null
    } catch (e: Exception) {
        AppLog.failure(where, e)
        lastError = Failure.of(e)
        null
    }
}

/** A failed load or action: [what] it was (null: a load), and why. */
data class ActionProblem(val what: String?, val failure: Failure)

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
                    onClick = { place = p; remote.clear() },
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
                    if (remote.hasProblem) {
                        item {
                            Text(
                                remote.problems.map { p ->
                                    val why = stringResource(p.failure.kind.sentence())
                                    if (p.what != null) "${p.what}: $why" else why
                                }.joinToString("\n"),
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
