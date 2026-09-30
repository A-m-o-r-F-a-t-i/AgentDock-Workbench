package dev.agentdock.workbench.lifecycle

import dev.agentdock.workbench.model.WorkbenchSettings
import org.junit.Assert.*
import org.junit.Test

class GuardianPolicyTest {
    @Test fun bootCheckIsReadOnlyAndIndependentOfPersistentGuardian() {
        for (guardian in listOf(false, true)) for (boot in listOf(false, true)) for (repair in listOf(false, true)) {
            val settings = WorkbenchSettings(guardianEnabled = guardian, bootHealthCheckEnabled = boot, autoRepairEnabled = repair)
            assertEquals(if (boot) "status" else null, GuardianPolicy.operation(settings, bootCheck = true))
        }
    }

    @Test fun pausedGuardianDoesNotDispatchAnyCheck() {
        val settings = WorkbenchSettings(guardianEnabled = true, guardianPaused = true, bootHealthCheckEnabled = true, autoRepairEnabled = true)
        assertNull(GuardianPolicy.operation(settings, false)); assertNull(GuardianPolicy.operation(settings, true))
    }

    @Test fun selectedRemoteEndpointNeverChangesLocalCheckOperation() {
        val settings = WorkbenchSettings(guardianEnabled = true, autoRepairEnabled = true)
        assertEquals("guardian_check", GuardianPolicy.operation(settings, false))
        assertEquals("guardian_check", GuardianPolicy.operation(settings.copy(endpoint = "https://remote.example", remoteEndpointEnabled = true), false))
        assertEquals("status", GuardianPolicy.operation(settings.copy(autoRepairEnabled = false), false))
    }

    @Test fun unacknowledgedStopSuppressesRepairWhileBridgeHandlesIt() {
        val settings = WorkbenchSettings(guardianEnabled = true, autoRepairEnabled = true,
            desiredNodeState = "stopped", nodeIntentOperationId = "stop")
        assertEquals("status", GuardianPolicy.operation(settings, false))
    }
}
