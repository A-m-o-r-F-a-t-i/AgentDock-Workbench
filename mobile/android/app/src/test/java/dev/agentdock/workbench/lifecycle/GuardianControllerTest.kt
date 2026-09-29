package dev.agentdock.workbench.lifecycle

import dev.agentdock.workbench.model.WorkbenchSettings
import kotlinx.coroutines.launch
import kotlinx.coroutines.test.runTest
import org.junit.Assert.*
import org.junit.Test

class GuardianControllerTest {
    private class Fixture : GuardianEffects {
        var settings = WorkbenchSettings()
        var permission = true
        var starts = 0
        var stops = 0
        var failStart = false
        val scheduled = mutableListOf<WorkbenchSettings>()
        val controller = GuardianController({ settings }, { settings = it(settings) }, this)
        override fun notificationsAllowed() = permission
        override fun configure(settings: WorkbenchSettings) { scheduled += settings }
        override fun start() {
            starts++
            if (failStart) { settings = settings.copy(theme = "dark"); error("injected start failure") }
        }
        override fun stop() { stops++ }
    }

    private suspend fun rejected(block: suspend () -> Unit) {
        var error: Exception? = null
        try { block() } catch (caught: IllegalStateException) { error = caught }
        assertNotNull("admission should fail", error)
    }

    @Test fun tileAndSettingsBothRequireNotifications() = runTest {
        val fixture = Fixture(); fixture.settings = fixture.settings.copy(notificationsEnabled = false)
        rejected { fixture.controller.setEnabled(true) }
        rejected { fixture.controller.toggle() }
        assertFalse(fixture.settings.guardianEnabled); assertEquals(0, fixture.starts)
    }

    @Test fun systemNotificationDenialCannotPartiallyEnableGuardian() = runTest {
        val fixture = Fixture(); fixture.permission = false
        rejected { fixture.controller.setEnabled(true) }
        assertFalse(fixture.settings.guardianEnabled); assertTrue(fixture.scheduled.isEmpty())
    }

    @Test fun failedStartRollsBackOnlyOwnedFields() = runTest {
        val fixture = Fixture(); fixture.failStart = true
        rejected { fixture.controller.setEnabled(true) }
        assertFalse(fixture.settings.guardianEnabled)
        assertEquals("dark", fixture.settings.theme)
        assertFalse(fixture.scheduled.last().guardianEnabled)
        assertEquals(1, fixture.stops)
    }

    @Test fun disablingNotificationsStopsGuardianButNotNodeIntent() = runTest {
        val fixture = Fixture(); fixture.settings = fixture.settings.copy(desiredNodeState = "running")
        fixture.controller.setEnabled(true)
        fixture.controller.setNotifications(false)
        assertFalse(fixture.settings.guardianEnabled); assertFalse(fixture.settings.notificationsEnabled)
        assertEquals("running", fixture.settings.desiredNodeState)
    }

    @Test fun pauseResumeAndToggleShareOneState() = runTest {
        val fixture = Fixture()
        fixture.controller.setEnabled(true); fixture.controller.setPaused(true)
        assertTrue(fixture.settings.guardianPaused)
        fixture.controller.toggle()
        assertTrue(GuardianPolicy.active(fixture.settings))
        fixture.permission = false
        fixture.controller.setPaused(true)
        rejected { fixture.controller.setPaused(false) }
        assertTrue(fixture.settings.guardianPaused)
    }

    @Test fun concurrentTogglesAreSerialized() = runTest {
        val fixture = Fixture()
        val one = launch { fixture.controller.toggle() }
        val two = launch { fixture.controller.toggle() }
        one.join(); two.join()
        assertTrue(fixture.settings.guardianEnabled); assertTrue(fixture.settings.guardianPaused)
        assertEquals(1, fixture.starts); assertEquals(1, fixture.stops)
    }
}
