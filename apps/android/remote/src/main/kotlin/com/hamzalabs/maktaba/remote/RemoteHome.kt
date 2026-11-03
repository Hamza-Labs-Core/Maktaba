package com.hamzalabs.maktaba.remote

import androidx.activity.compose.BackHandler
import androidx.annotation.StringRes
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.LazyListScope
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material.icons.outlined.Dns
import androidx.compose.material.icons.outlined.ReportProblem
import androidx.compose.material.icons.outlined.Sync
import androidx.compose.material.icons.outlined.SystemUpdate
import androidx.compose.material.icons.outlined.VideoLibrary
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Text
import androidx.compose.material3.TopAppBar
import androidx.compose.foundation.lazy.rememberLazyListState
import androidx.compose.material3.adaptive.WindowAdaptiveInfo
import androidx.compose.material3.adaptive.currentWindowAdaptiveInfoV2
import androidx.compose.material3.adaptive.navigationsuite.NavigationSuiteScaffold
import androidx.compose.material3.adaptive.navigationsuite.NavigationSuiteScaffoldDefaults
import androidx.compose.material3.adaptive.navigationsuite.NavigationSuiteScope
import androidx.compose.material3.pulltorefresh.PullToRefreshBox
import androidx.compose.runtime.Composable
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.MutableState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.saveable.rememberSaveableStateHolder
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.testTag
import com.hamzalabs.maktaba.core.layout.LocalAdaptiveInfo
import androidx.compose.ui.platform.LocalConfiguration
import com.hamzalabs.maktaba.core.ui.NavigationLabel
import com.hamzalabs.maktaba.core.ui.rememberNavigationLabelSizes
import com.hamzalabs.maktaba.core.ui.navigationLabelsFit
import com.hamzalabs.maktaba.core.ui.Revalidating
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.unit.dp
import com.hamzalabs.maktaba.core.api.ApiClient
import com.hamzalabs.maktaba.core.api.ApiException
import com.hamzalabs.maktaba.core.api.Failure
import com.hamzalabs.maktaba.core.diagnostics.AppLog
import com.hamzalabs.maktaba.core.api.User
import com.hamzalabs.maktaba.core.layout.HingeSplit
import com.hamzalabs.maktaba.core.layout.ListDetailSplit
import com.hamzalabs.maktaba.core.layout.ListPaneWidth
import com.hamzalabs.maktaba.core.layout.Panes
import com.hamzalabs.maktaba.core.layout.currentPanes
import com.hamzalabs.maktaba.core.session.AppSession
import com.hamzalabs.maktaba.core.ui.AccountMenu
import com.hamzalabs.maktaba.core.ui.ApproveTvButton
import com.hamzalabs.maktaba.core.ui.PendingSignInsHost
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.layout.BoxWithConstraints
import com.hamzalabs.maktaba.core.ui.Message
import com.hamzalabs.maktaba.core.ui.ThemeStore
import com.hamzalabs.maktaba.core.ui.sentence
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.launch
import java.util.concurrent.atomic.AtomicInteger
import com.hamzalabs.maktaba.core.R as CoreR

/**
 * The Remote's places: what needs attention first, as on the web's queue page. The home's
 * processes are on Machines, the jobs running now on Queue.
 */
enum class Place(@StringRes val label: Int, val icon: ImageVector, @StringRes val short: Int = label) {
    // the bar's label is one word: five items share a cover screen ~70 dp each
    Attention(R.string.place_attention, Icons.Outlined.ReportProblem, R.string.place_attention_short),
    Queue(R.string.place_queue, Icons.Outlined.Sync),
    Updates(R.string.place_updates, Icons.Outlined.SystemUpdate),
    Libraries(R.string.place_libraries, Icons.Outlined.VideoLibrary),
    Machines(R.string.place_machines, Icons.Outlined.Dns),
}

/**
 * What every Remote screen needs: the client, and how to report a failure or a sign-out.
 * A screen's loads and its actions fail apart: the next poll that works clears a load's
 * failure, never an action's (that stays until another action, a refresh or another place).
 */
class Remote(val client: ApiClient, private val session: AppSession) {
    private val kept = mutableMapOf<String, MutableState<*>>()

    /**
     * A place's state under [key], kept for as long as this home is signed in: another place
     * and back (or a fold) shows what was read last at once, and the next read updates it.
     */
    @Suppress("UNCHECKED_CAST")
    fun <T> kept(key: String, initial: () -> T): MutableState<T> =
        kept.getOrPut(key) { mutableStateOf(initial()) } as MutableState<T>

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

    /**
     * Bumped as each action starts and once it is done. A read that began before an action
     * ended was answered before its save, so it is dropped (null, no problem shown): applied,
     * it would put the old state back on screen while the home has the new one. The screen
     * reads again after the action.
     */
    private val actions = AtomicInteger()

    /** Reads the home's state: a failure shows until a read works again. */
    suspend fun <T> load(block: suspend ApiClient.() -> T): T? {
        val seen = actions.get()
        val got = attempt("remote load", block)
        if (actions.get() != seen) return null
        loadProblem = if (got != null) null else lastError
        return got
    }

    /** Changes the home: each failure is kept and shown, labelled by [what] when given. */
    suspend fun act(what: String? = null, block: suspend ApiClient.() -> Unit): Boolean {
        actions.incrementAndGet()
        try {
            if (attempt("remote action", block) != null) return true
            val why = lastError ?: return false
            actionProblems = actionProblems + ActionProblem(what, why)
            return false
        } finally {
            actions.incrementAndGet()
        }
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
    val remote = remember(client) { Remote(client, session) }
    // a TV waiting to sign in: a banner above every place, and a notification
    PendingSignInsHost(session, user, client) {
        RemoteFrame(remote, account = { RemoteAccount(session, user, themes, client) })
    }
}

/**
 * The end of every Remote place's top bar: "Approve a TV" in sight (a TV showing a sign-in
 * code is approved here), then the account menu, which keeps its own "Approve a sign-in".
 */
@Composable
fun RemoteAccount(session: AppSession, user: User, themes: ThemeStore, client: ApiClient) {
    ApproveTvButton(session, user, client)
    AccountMenu(session, user, themes)
}

/**
 * The frame for [remote]: the places in the navigation suite. Each place keeps what it read
 * ([Remote.kept]) and its scroll position (a saveable state per place), so switching shows the
 * last page at once while it reads again.
 */
@Composable
internal fun RemoteFrame(
    remote: Remote,
    account: @Composable () -> Unit,
    adaptiveInfo: WindowAdaptiveInfo = currentWindowAdaptiveInfoV2(),
    screens: @Composable (Place) -> Unit = { place -> PlaceScreen(place, remote, account) },
) {
    var place by rememberSaveable { mutableStateOf(Place.Attention) }
    val places = rememberSaveableStateHolder()
    val labels = Place.entries.map { stringResource(it.short) }
    // a cover screen at a large text size: the selected label only (see navigationLabelsFit)
    val allLabels = navigationLabelsFit(labels, LocalConfiguration.current.screenWidthDp)
    val labelSizes = rememberNavigationLabelSizes(labels)
    val items: NavigationSuiteScope.() -> Unit = {
        Place.entries.forEachIndexed { i, p ->
            item(
                selected = p == place,
                onClick = { if (p != place) { place = p; remote.clear() } },
                icon = { Icon(p.icon, null) },
                label = { NavigationLabel(labels[i], labelSizes) },
                alwaysShowLabel = allLabels,
                modifier = Modifier.testTag("place-${p.name}"),
            )
        }
    }
    // the panes follow the same window (and hinge) as the navigation
    CompositionLocalProvider(LocalAdaptiveInfo provides adaptiveInfo) {
        NavigationSuiteScaffold(
            navigationSuiteItems = items,
            layoutType = NavigationSuiteScaffoldDefaults.calculateFromAdaptiveInfo(adaptiveInfo),
        ) {
            places.SaveableStateProvider(place.name) { screens(place) }
        }
    }
}

@Composable
private fun PlaceScreen(place: Place, remote: Remote, account: @Composable () -> Unit) {
    when (place) {
        Place.Attention -> AttentionScreen(remote, account)
        Place.Queue -> QueueScreen(remote, account)
        Place.Updates -> UpdatesScreen(remote, account)
        Place.Libraries -> LibrariesScreen(remote, account)
        Place.Machines -> MachinesScreen(remote, account)
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
    revalidating: Boolean = false,
    rows: LazyListScope.() -> Unit,
) {
    val panes = currentPanes()
    val scope = rememberCoroutineScope()
    // here, not in the list: the list is drawn in another place when the window folds or unfolds
    // (one pane, beside the page, at the hinge), and its position goes with it
    val listState = rememberLazyListState()
    val list: @Composable (Modifier) -> Unit = { modifier ->
        Scaffold(
            modifier = modifier,
            topBar = {
                // a cover screen at a large text size: the title ends in "…" and the place's
                // own action (Retry all, Pause all) gets at most 40% of the bar, so "Approve a
                // TV" and the account menu (measured first: the weight) never leave the bar
                BoxWithConstraints {
                    val most = maxWidth * 0.4f
                    TopAppBar(
                        title = { Text(title, maxLines = 1, overflow = TextOverflow.Ellipsis, modifier = Modifier.testTag("bar-title")) },
                        actions = {
                            Box(Modifier.weight(1f, fill = false).widthIn(max = most)) { actions() }
                            account()
                        },
                    )
                }
            },
        ) { padding ->
            PullToRefreshBox(
                isRefreshing = refreshing,
                onRefresh = { scope.launch { onRefresh() } },
                modifier = Modifier.padding(padding).fillMaxSize(),
            ) {
                LazyColumn(Modifier.fillMaxSize().testTag("rows"), state = listState) {
                    if (remote.hasProblem) {
                        item {
                            Text(
                                remote.problems.map { p ->
                                    val why = stringResource(p.failure.kind.sentence())
                                    if (p.what != null) stringResource(R.string.say_label_value, p.what, why) else why
                                }.joinToString("\n"),
                                color = MaterialTheme.colorScheme.error,
                                modifier = Modifier.fillMaxWidth().padding(16.dp),
                            )
                        }
                    }
                    rows()
                }
                // the rows read last stay; a thin bar says they are being read again
                Revalidating(revalidating && !refreshing, Modifier.align(Alignment.TopCenter))
            }
        }
    }
    val page: @Composable (back: Boolean) -> Unit = { back ->
        Scaffold(
            topBar = {
                TopAppBar(
                    title = { Text(chosenTitle ?: "") },
                    navigationIcon = {
                        if (back) IconButton(onClick = onUnchoose) { Icon(Icons.AutoMirrored.Filled.ArrowBack, stringResource(CoreR.string.error_back)) }
                    },
                )
            },
        ) { padding ->
            Box(Modifier.padding(padding).fillMaxSize()) {
                if (detail != null) detail() else Message(stringResource(R.string.choose_one))
            }
        }
    }
    when (panes) {
        // on a table: the page on the upright half, the list on the flat one (touched)
        is Panes.Tabletop -> HingeSplit(panes, first = { page(detail != null) }, second = { list(Modifier.fillMaxSize()) })
        // like a book: the list on the start page (beside the rail), the page on the other, as the Player
        is Panes.Book -> HingeSplit(panes, first = { list(Modifier.fillMaxSize()) }, second = { page(false) })
        // the divider between them can be dragged; one split for every Remote screen
        Panes.ListDetail -> ListDetailSplit(
            key = "remote",
            preferredList = ListPaneWidth + 40.dp,
            list = { list(Modifier.fillMaxSize()) },
            detail = { page(false) },
        )
        Panes.One -> if (detail != null) {
            BackHandler(onBack = onUnchoose)
            page(true)
        } else {
            list(Modifier.fillMaxSize())
        }
    }
}
