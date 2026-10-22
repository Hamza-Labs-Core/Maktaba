package com.hamzalabs.maktaba.tv.ui.components

import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.unit.dp
import androidx.tv.material3.ExperimentalTvMaterial3Api
import androidx.tv.material3.MaterialTheme
import androidx.tv.material3.Tab
import androidx.tv.material3.TabDefaults
import androidx.tv.material3.TabRow
import androidx.tv.material3.TabRowDefaults
import androidx.tv.material3.Text
import com.hamzalabs.maktaba.tv.ui.theme.MaktabaPalette

/**
 * Branded header with the primary navigation tabs. On Android TV the
 * tab row sits along the top and is itself focusable — pressing "up"
 * from the content moves focus into the tabs, matching Google TV apps.
 *
 * The bar is a translucent nav band so it stays readable when Home draws
 * the library art behind it. The pill under the selected tab turns gold
 * (with ink text) while the tab row has focus.
 */
@OptIn(ExperimentalTvMaterial3Api::class)
@Composable
fun TopBar(
    tabs: List<String>,
    selectedIndex: Int,
    onSelect: (Int) -> Unit,
    modifier: Modifier = Modifier,
) {
    Row(
        modifier = modifier
            .fillMaxWidth()
            .background(MaktabaPalette.NavTranslucent)
            .padding(horizontal = 48.dp, vertical = 20.dp),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(32.dp),
    ) {
        Text(
            text = "۞ Maktaba",
            style = MaterialTheme.typography.titleLarge,
            color = MaktabaPalette.Gold,
        )
        Spacer(Modifier.weight(1f))
        TabRow(
            selectedTabIndex = selectedIndex,
            indicator = { tabPositions, doesTabRowHaveFocus ->
                tabPositions.getOrNull(selectedIndex)?.let { position ->
                    TabRowDefaults.PillIndicator(
                        currentTabPosition = position,
                        doesTabRowHaveFocus = doesTabRowHaveFocus,
                        activeColor = MaktabaPalette.Gold,
                        inactiveColor = MaktabaPalette.Raised,
                    )
                }
            },
        ) {
            tabs.forEachIndexed { index, label ->
                Tab(
                    selected = index == selectedIndex,
                    onFocus = { onSelect(index) },
                    colors = TabDefaults.pillIndicatorTabColors(
                        contentColor = MaktabaPalette.Muted,
                        inactiveContentColor = MaktabaPalette.Muted,
                        selectedContentColor = MaktabaPalette.Gold,
                        focusedContentColor = MaktabaPalette.OnGold,
                    ),
                ) {
                    Text(
                        text = label,
                        style = MaterialTheme.typography.labelLarge,
                        modifier = Modifier.padding(horizontal = 16.dp, vertical = 6.dp),
                    )
                }
            }
        }
    }
}
