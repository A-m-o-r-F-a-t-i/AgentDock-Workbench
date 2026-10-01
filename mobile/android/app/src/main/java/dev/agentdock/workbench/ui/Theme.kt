package dev.agentdock.workbench.ui

import androidx.activity.compose.LocalActivity
import androidx.compose.foundation.isSystemInDarkTheme
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.darkColorScheme
import androidx.compose.material3.lightColorScheme
import androidx.compose.runtime.Composable
import androidx.compose.runtime.SideEffect
import androidx.compose.ui.graphics.toArgb
import androidx.compose.ui.platform.LocalView

internal fun resolveWorkbenchDarkTheme(theme: String, systemDark: Boolean): Boolean = when (theme) {
    "dark" -> true
    "light" -> false
    else -> systemDark
}

@Composable
fun WorkbenchTheme(theme: String, content: @Composable () -> Unit) {
    val dark = resolveWorkbenchDarkTheme(theme, isSystemInDarkTheme())
    val colors = if (dark) darkColorScheme() else lightColorScheme()
    val window = LocalActivity.current?.window
    val view = LocalView.current
    SideEffect {
        if (window != null && !view.isInEditMode) {
            applyWorkbenchSystemBars(window, dark, colors.surface.toArgb())
        }
    }
    MaterialTheme(colorScheme = colors, content = content)
}
