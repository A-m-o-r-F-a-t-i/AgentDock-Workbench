package dev.agentdock.workbench.ui

import android.graphics.drawable.ColorDrawable
import android.os.Build
import android.view.Window
import androidx.annotation.ColorInt
import androidx.core.view.WindowCompat

@Suppress("DEPRECATION") // Colors are needed on API 26-34; API 35+ owns edge-to-edge bars.
internal fun applyWorkbenchSystemBars(window: Window, dark: Boolean, @ColorInt surfaceColor: Int) {
    val controller = WindowCompat.getInsetsController(window, window.decorView)
    controller.isAppearanceLightStatusBars = !dark
    controller.isAppearanceLightNavigationBars = !dark
    if (Build.VERSION.SDK_INT < 35) {
        window.statusBarColor = surfaceColor
        window.navigationBarColor = surfaceColor
    }
    // The same Material surface backs transparent system bars and startup gaps.
    // Preserve existing inset handling and the OS three-button contrast scrim.
    window.setBackgroundDrawable(ColorDrawable(surfaceColor))
}
