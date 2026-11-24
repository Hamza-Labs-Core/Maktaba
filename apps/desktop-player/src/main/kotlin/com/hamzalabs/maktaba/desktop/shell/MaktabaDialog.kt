// The Player's one dialog style (Epic 13 story 13.15; the design:
// docs/design/desktop/README.md, "Components", "Dialogs"): the raised surface with its edge and
// 14 px corners, the title in the text face at 600, the words in the muted colour, and the
// buttons in a foot of their own, the dismiss button before the confirm one. Every dialog of
// the Player takes this in place of Material's AlertDialog, with the same parameters.
package com.hamzalabs.maktaba.desktop.shell

import androidx.compose.foundation.BorderStroke
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.BasicAlertDialog
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.LocalContentColor
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.ProvideTextStyle
import androidx.compose.material3.Surface
import androidx.compose.runtime.Composable
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import com.hamzalabs.maktaba.desktop.Tokens

/** The dialog's colours, from the token build (Tokens.kt: desktop.raised, pane, line). */
object DialogColors {
    val Raised = Tokens.Desktop.raised
    val Edge = Tokens.Desktop.line
    val Pane = Tokens.Desktop.pane
    val Line = Tokens.Desktop.line
    val Fg = Tokens.Ink.fg
    val Muted = Tokens.Ink.muted
}

/** Material's AlertDialog's parameters, drawn in the Player's own style. */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun MaktabaAlertDialog(
    onDismissRequest: () -> Unit,
    confirmButton: @Composable () -> Unit,
    modifier: Modifier = Modifier,
    dismissButton: (@Composable () -> Unit)? = null,
    title: (@Composable () -> Unit)? = null,
    text: (@Composable () -> Unit)? = null,
) {
    BasicAlertDialog(onDismissRequest = onDismissRequest, modifier = modifier) {
        Surface(
            shape = RoundedCornerShape(14.dp),
            color = DialogColors.Raised,
            contentColor = DialogColors.Fg,
            border = BorderStroke(1.dp, DialogColors.Edge),
            shadowElevation = 16.dp,
            modifier = Modifier.widthIn(min = 360.dp, max = 560.dp).testTag("maktaba-dialog"),
        ) {
            Column {
                // the words give way first, as in Material's dialog: a tall body (a report's
                // contents, big screen on a short window) never pushes the buttons off screen
                Column(
                    Modifier.weight(1f, fill = false)
                        .padding(start = 24.dp, end = 24.dp, top = 24.dp, bottom = 20.dp),
                ) {
                    title?.let {
                        ProvideTextStyle(
                            MaterialTheme.typography.titleMedium.copy(fontSize = 18.sp, fontWeight = FontWeight.SemiBold),
                        ) { it() }
                        Spacer(Modifier.height(8.dp))
                    }
                    text?.let {
                        CompositionLocalProvider(LocalContentColor provides DialogColors.Muted) {
                            ProvideTextStyle(MaterialTheme.typography.bodyMedium) { it() }
                        }
                    }
                }
                HorizontalDivider(color = DialogColors.Line)
                Row(
                    modifier = Modifier.fillMaxWidth().background(DialogColors.Pane).padding(horizontal = 24.dp, vertical = 14.dp),
                    horizontalArrangement = Arrangement.spacedBy(10.dp, Alignment.End),
                    verticalAlignment = Alignment.CenterVertically,
                ) {
                    dismissButton?.invoke()
                    confirmButton()
                }
            }
        }
    }
}
