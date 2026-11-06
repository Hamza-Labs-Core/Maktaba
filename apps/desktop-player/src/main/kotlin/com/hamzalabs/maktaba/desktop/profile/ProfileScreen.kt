package com.hamzalabs.maktaba.desktop.profile

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.BoxWithConstraints
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.FlowRow
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxHeight
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.FilterChip
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.NavigationDrawerItem
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.heading
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.unit.dp
import com.hamzalabs.maktaba.desktop.LocalTexts
import com.hamzalabs.maktaba.desktop.shell.DesktopHome
import com.hamzalabs.maktaba.desktop.shell.LocalDesktopHome

/** A window this wide puts the groups beside the page; narrower, one column. */
val PROFILE_WIDE = 900.dp

/** Profile's groups, in their order (story 55.09). */
enum class ProfileGroup(val key: String) {
    Account("profile.account"),
    History("profile.history"),
    Favorites("profile.favorites"),
    Playlists("profile.playlists"),
    Sessions("profile.sessions"),
    Approve("profile.approve"),
    Playback("profile.playback"),
    Language("profile.language"),
    Help("profile.help"),
}

/** The group shown, kept while the home is signed in. */
class ProfileNav {
    var group by mutableStateOf(ProfileGroup.Account)
}

/** This home's Profile state, kept while it is signed in. */
fun DesktopHome.profileState(): ProfileState = keep("profile") { ProfileState(client, ::signedOut) }

/** The Profile section (story 55.09). */
@Composable
fun ProfileScreen() {
    val home = LocalDesktopHome.current
    ProfileContent(home.profileState(), home, home.keep("profile-nav") { ProfileNav() })
}

@Composable
fun ProfileContent(state: ProfileState, home: DesktopHome, nav: ProfileNav) {
    val t = LocalTexts.current
    // sessions are not shown on a home without the route
    val groups = ProfileGroup.entries.filter { it != ProfileGroup.Sessions || !state.sessions.missing }
    val choose = { g: ProfileGroup -> if (g == ProfileGroup.Approve) home.actions.approve() else nav.group = g }
    BoxWithConstraints(Modifier.fillMaxSize().testTag("profile")) {
        if (maxWidth >= PROFILE_WIDE) {
            Row(Modifier.fillMaxSize()) {
                Column(Modifier.width(260.dp).fillMaxHeight().verticalScroll(rememberScrollState()).padding(8.dp).testTag("profile-groups")) {
                    for (g in groups) {
                        NavigationDrawerItem(
                            label = { Text(t[g.key]) },
                            selected = nav.group == g,
                            onClick = { choose(g) },
                            modifier = Modifier.testTag("group-${g.name}"),
                        )
                    }
                }
                Column(Modifier.weight(1f).fillMaxHeight().padding(16.dp)) { GroupPage(nav.group, state, home) }
            }
        } else {
            Column(Modifier.fillMaxSize().padding(16.dp).testTag("profile-column"), verticalArrangement = Arrangement.spacedBy(12.dp)) {
                FlowRow(horizontalArrangement = Arrangement.spacedBy(8.dp), verticalArrangement = Arrangement.spacedBy(4.dp)) {
                    for (g in groups) FilterChip(nav.group == g, { choose(g) }, label = { Text(t[g.key]) }, modifier = Modifier.testTag("group-${g.name}"))
                }
                Column(Modifier.weight(1f)) { GroupPage(nav.group, state, home) }
            }
        }
    }
}

@Composable
private fun GroupPage(group: ProfileGroup, state: ProfileState, home: DesktopHome) {
    val t = LocalTexts.current
    Column(Modifier.fillMaxSize().testTag("page-${group.name}"), verticalArrangement = Arrangement.spacedBy(12.dp)) {
        Text(t[group.key], style = MaterialTheme.typography.headlineSmall, modifier = Modifier.semantics { heading() })
        when (group) {
            ProfileGroup.Account -> AccountPage(state, home)
            ProfileGroup.History -> HistoryPage(state, home)
            ProfileGroup.Favorites -> FavoritesPage(state, home)
            ProfileGroup.Playlists -> PlaylistsPage(state, home)
            ProfileGroup.Sessions -> SessionsPage(state)
            ProfileGroup.Approve -> AccountPage(state, home)
            ProfileGroup.Playback -> PlaybackSettingsPage(home.prefs.playback)
            ProfileGroup.Language -> LanguagePage(home)
            ProfileGroup.Help -> HelpPage(state, home)
        }
    }
}
