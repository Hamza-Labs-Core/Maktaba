package com.hamzalabs.maktaba.desktop.profile

import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.size
import androidx.compose.ui.Modifier
import androidx.compose.ui.test.ExperimentalTestApi
import androidx.compose.ui.test.hasTestTag
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import androidx.compose.ui.test.runComposeUiTest
import androidx.compose.ui.unit.dp
import com.hamzalabs.maktaba.desktop.InPlayer
import com.hamzalabs.maktaba.desktop.Lang
import com.hamzalabs.maktaba.desktop.TestHome
import com.hamzalabs.maktaba.desktop.shell.DesktopHome
import com.hamzalabs.maktaba.desktop.shell.HomeActions
import org.junit.Assert.assertEquals
import org.junit.Test

/** Story 55.09 T07: groups and labels; one column when narrow. */
@OptIn(ExperimentalTestApi::class)
class ProfileScreenTest {
    @Test
    fun wideShowsTheGroupsBesideThePageNarrowOneColumn() = TestHome().use { f ->
        val home = f.home()
        runComposeUiTest {
            setContent { InPlayer(home) { Box(Modifier.size(1200.dp, 800.dp)) { ProfileContent(ProfileState(f.client), home, ProfileNav()) } } }
            onNodeWithTag("profile-groups").assertExists()
            onNodeWithText("amina on Kitchen").assertExists()
        }
        runComposeUiTest {
            setContent { InPlayer(home, Lang.Ar) { Box(Modifier.size(600.dp, 800.dp)) { ProfileContent(ProfileState(f.client), home, ProfileNav()) } } }
            onNodeWithTag("profile-column").assertExists()
            assertEquals(0, onAllNodes(hasTestTag("profile-groups")).fetchSemanticsNodes().size)
            onNodeWithText("سجل المشاهدة").assertExists()
            onNodeWithText("المساعدة وحول التطبيق").assertExists()
        }
    }

    @Test
    fun signOutAsksFirst() = TestHome().use { f ->
        var signedOut = 0
        val home = DesktopHome(
            com.hamzalabs.maktaba.core.api.User("u1", "amina"), f.client, "Kitchen", f.prefs,
            kotlinx.coroutines.CoroutineScope(kotlinx.coroutines.Dispatchers.IO),
            posters = com.hamzalabs.maktaba.desktop.Posters(null),
            actions = HomeActions(signOut = { signedOut++ }),
        )
        runComposeUiTest {
            setContent { InPlayer(home) { Box(Modifier.size(1200.dp, 800.dp)) { ProfileContent(ProfileState(f.client), home, ProfileNav()) } } }
            onNodeWithTag("sign-out").performClick()
            onNodeWithText("Sign out of Kitchen?").assertExists()
            assertEquals(0, signedOut)
            onNodeWithTag("sign-out-confirm").performClick()
            assertEquals(1, signedOut)
        }
    }

    @Test
    fun languageSwitchesAtOnceAndIsKept() = TestHome().use { f ->
        val home = f.home()
        val nav = ProfileNav().apply { group = ProfileGroup.Language }
        runComposeUiTest {
            setContent { InPlayer(home, home.prefs.lang) { Box(Modifier.size(1200.dp, 800.dp)) { ProfileContent(ProfileState(f.client), home, nav) } } }
            onNodeWithTag("lang-ar").performClick()
            assertEquals(Lang.Ar, home.prefs.lang)
            assertEquals("ar", home.prefs.store.get("lang"))
        }
    }
}
