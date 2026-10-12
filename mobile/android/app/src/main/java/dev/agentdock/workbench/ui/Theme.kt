package dev.agentdock.workbench.ui

import androidx.activity.compose.LocalActivity
import androidx.compose.foundation.isSystemInDarkTheme
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.Shapes
import androidx.compose.material3.Typography
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
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
    val colors = if (dark) darkColorScheme(
        primary = Color(0xFFD4C6EE), onPrimary = Color(0xFF352447), primaryContainer = Color(0xFF493858), onPrimaryContainer = Color(0xFFEDE2FA),
        secondary = Color(0xFFC9C0CE), secondaryContainer = Color(0xFF39333F), onSecondaryContainer = Color(0xFFF0E8F4),
        background = Color(0xFF171619), surface = Color(0xFF201E23), onBackground = Color(0xFFEAE6ED), onSurface = Color(0xFFEAE6ED),
        surfaceVariant = Color(0xFF35313B), onSurfaceVariant = Color(0xFFC8C1CD), outline = Color(0xFF918994), outlineVariant = Color(0xFF444047),
        surfaceContainer = Color(0xFF242127), surfaceContainerLow = Color(0xFF201D23), surfaceContainerHigh = Color(0xFF302B33)
    ) else lightColorScheme(
        primary = Color(0xFF68537F), onPrimary = Color.White, primaryContainer = Color(0xFFEAE0F3), onPrimaryContainer = Color(0xFF30203F),
        secondary = Color(0xFF665D70), secondaryContainer = Color(0xFFEEE8F2), onSecondaryContainer = Color(0xFF302A37),
        background = Color(0xFFFAF9F6), surface = Color(0xFFFFFDFC), onBackground = Color(0xFF25222A), onSurface = Color(0xFF25222A),
        surfaceVariant = Color(0xFFF0EBF1), onSurfaceVariant = Color(0xFF69626D), outline = Color(0xFF867F8A), outlineVariant = Color(0xFFDFD9E2),
        surfaceContainer = Color(0xFFF2EEF3), surfaceContainerLow = Color(0xFFF7F4F7), surfaceContainerHigh = Color(0xFFECE7EF)
    )
    val window = LocalActivity.current?.window
    val view = LocalView.current
    SideEffect {
        if (window != null && !view.isInEditMode) {
            applyWorkbenchSystemBars(window, dark, colors.surface.toArgb())
        }
    }
    val baseType = Typography()
    MaterialTheme(colorScheme = colors,
        typography = baseType.copy(
            titleLarge = baseType.titleLarge.copy(fontSize = 22.sp, lineHeight = 30.sp),
            titleMedium = baseType.titleMedium.copy(fontSize = 16.sp, lineHeight = 24.sp),
            bodyLarge = baseType.bodyLarge.copy(fontSize = 16.sp, lineHeight = 24.sp),
            bodyMedium = baseType.bodyMedium.copy(fontSize = 14.sp, lineHeight = 22.sp)),
        shapes = Shapes(small = RoundedCornerShape(12.dp), medium = RoundedCornerShape(18.dp), large = RoundedCornerShape(24.dp)),
        content = content)
}
