package com.hamzalabs.maktaba.player.ui.components

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.LazyListScope
import androidx.compose.foundation.lazy.LazyListState
import androidx.compose.foundation.lazy.LazyRow
import androidx.compose.foundation.lazy.rememberLazyListState
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.semantics.heading
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.tooling.preview.Preview
import androidx.compose.ui.unit.dp
import com.hamzalabs.maktaba.core.ui.MaktabaTheme
import com.hamzalabs.maktaba.core.ui.MaktabaTokens
import com.hamzalabs.maktaba.core.ui.ThemeMode
import com.hamzalabs.maktaba.player.R

/**
 * A titled row of cards that scrolls sideways (Home's rows, a library's hub rows, FR-10 / FR-15):
 * the title as a heading, "See all" when [onSeeAll] is given, then [content] in a [LazyRow]. The
 * row starts at the start edge, so on the right in Arabic (NFR-11); cards align to the top, so a
 * card whose title wraps at a large font makes the row taller instead of cutting it (NFR-7).
 * A screen shows a rail only when it has cards (FR-10: empty rows are hidden).
 */
@Composable
fun Rail(
    title: String,
    modifier: Modifier = Modifier,
    onSeeAll: (() -> Unit)? = null,
    state: LazyListState = rememberLazyListState(),
    contentPadding: PaddingValues = PaddingValues(horizontal = MaktabaTokens.Spacing.s4),
    content: LazyListScope.() -> Unit,
) {
    Column(modifier.fillMaxWidth(), verticalArrangement = Arrangement.spacedBy(MaktabaTokens.Spacing.s2)) {
        Row(
            Modifier.fillMaxWidth().heightIn(min = 48.dp).padding(start = MaktabaTokens.Spacing.s4, end = MaktabaTokens.Spacing.s2),
            verticalAlignment = Alignment.CenterVertically,
        ) {
            Text(
                title,
                Modifier.weight(1f).semantics { heading() },
                style = MaterialTheme.typography.titleLarge,
                color = MaterialTheme.colorScheme.onBackground,
            )
            if (onSeeAll != null) {
                TextButton(onClick = onSeeAll, modifier = Modifier.heightIn(min = 48.dp)) {
                    Text(stringResource(R.string.components_see_all))
                }
            }
        }
        LazyRow(
            Modifier.fillMaxWidth().testTag("rail-row"),
            state = state,
            contentPadding = contentPadding,
            horizontalArrangement = Arrangement.spacedBy(MaktabaTokens.Spacing.s3),
            verticalAlignment = Alignment.Top,
            content = content,
        )
    }
}

@Preview(name = "Rail", backgroundColor = 0xFF0B111C, showBackground = true, widthDp = 400)
@Composable
private fun RailPreview() = MaktabaTheme(ThemeMode.Dark) {
    Rail("Continue Watching", onSeeAll = {}) {
        items(4) { n -> PosterCard("Lesson ${n + 1}", posterUrl = null, onClick = {}, progress = 0.25f * n) }
    }
}
