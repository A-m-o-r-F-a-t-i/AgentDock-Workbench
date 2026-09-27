package dev.agentdock.workbench.lifecycle

import dev.agentdock.workbench.model.WorkbenchSettings
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Test

class GuardianConstraintPolicyTest {
    @Test
    fun noConstraintsRunWithoutNetworkOrCharging() {
        assertNull(GuardianConstraintPolicy.waitReason(WorkbenchSettings(), GuardianConditionSnapshot(false, false)))
    }

    @Test
    fun wifiAndChargingConstraintsAreIndependentAndOrdered() {
        val settings = WorkbenchSettings(onlyOnWifi = true, onlyWhileCharging = true)
        assertEquals("等待已验证的 Wi-Fi 连接", GuardianConstraintPolicy.waitReason(settings, GuardianConditionSnapshot(false, false)))
        assertEquals("等待设备充电", GuardianConstraintPolicy.waitReason(settings, GuardianConditionSnapshot(true, false)))
        assertNull(GuardianConstraintPolicy.waitReason(settings, GuardianConditionSnapshot(true, true)))
    }
}
