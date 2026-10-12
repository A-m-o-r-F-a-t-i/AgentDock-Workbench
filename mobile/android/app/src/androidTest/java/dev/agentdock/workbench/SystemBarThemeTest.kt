package dev.agentdock.workbench

import android.content.res.Configuration
import android.os.Build
import androidx.activity.ComponentActivity
import androidx.compose.material3.Text
import androidx.compose.material3.MaterialTheme
import androidx.compose.runtime.SideEffect
import androidx.compose.runtime.mutableStateOf
import androidx.compose.ui.graphics.toArgb
import androidx.compose.ui.test.junit4.createAndroidComposeRule
import androidx.core.view.WindowCompat
import androidx.test.ext.junit.runners.AndroidJUnit4
import dev.agentdock.workbench.ui.WorkbenchTheme
import dev.agentdock.workbench.ui.resolveWorkbenchDarkTheme
import org.junit.Assert.assertEquals
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith

@RunWith(AndroidJUnit4::class)
class SystemBarThemeTest {
    @get:Rule val compose = createAndroidComposeRule<ComponentActivity>()

    @Suppress("DEPRECATION")
    @Test fun systemBarsFollowLiveAppThemeWithoutCoreOrTermux() {
        val selection = mutableStateOf("dark")
        var appliedSurface = 0
        compose.setContent {
            WorkbenchTheme(selection.value) {
                val surface = MaterialTheme.colorScheme.surface.toArgb()
                SideEffect { appliedSurface = surface }
                Text("Native theme regression")
            }
        }
        for (theme in listOf("dark", "light", "system", "dark")) {
            compose.runOnIdle { selection.value = theme }
            compose.waitForIdle()
            compose.runOnIdle {
                val activity = compose.activity
                val systemDark = activity.resources.configuration.uiMode and Configuration.UI_MODE_NIGHT_MASK == Configuration.UI_MODE_NIGHT_YES
                val dark = resolveWorkbenchDarkTheme(theme, systemDark)
                val window = activity.window
                val controller = WindowCompat.getInsetsController(window, window.decorView)
                assertEquals(!dark, controller.isAppearanceLightStatusBars)
                assertEquals(!dark, controller.isAppearanceLightNavigationBars)
                if (Build.VERSION.SDK_INT < 35) {
                    // Follow the actual application palette, not Material3's
                    // unrelated default palette. Both system bars must update.
                    assertEquals(appliedSurface, window.statusBarColor)
                    assertEquals(appliedSurface, window.navigationBarColor)
                }
            }
        }
    }
}
