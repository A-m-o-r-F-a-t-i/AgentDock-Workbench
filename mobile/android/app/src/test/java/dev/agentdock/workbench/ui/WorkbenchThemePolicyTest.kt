package dev.agentdock.workbench.ui

import org.junit.Assert.assertEquals
import org.junit.Test

class WorkbenchThemePolicyTest {
    @Test fun explicitThemesOverrideEitherSystemMode() {
        for (systemDark in listOf(false, true)) {
            assertEquals(true, resolveWorkbenchDarkTheme("dark", systemDark))
            assertEquals(false, resolveWorkbenchDarkTheme("light", systemDark))
            assertEquals(systemDark, resolveWorkbenchDarkTheme("system", systemDark))
            assertEquals(systemDark, resolveWorkbenchDarkTheme("unknown", systemDark))
        }
    }
}
