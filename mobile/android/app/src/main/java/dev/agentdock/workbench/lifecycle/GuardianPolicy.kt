package dev.agentdock.workbench.lifecycle

import dev.agentdock.workbench.model.WorkbenchSettings

object GuardianPolicy {
    fun active(settings: WorkbenchSettings): Boolean = settings.guardianEnabled && !settings.guardianPaused

    /** The bridge checks the local node's actual intent, identity and recovery budget. */
    fun operation(settings: WorkbenchSettings, bootCheck: Boolean): String? = when {
        settings.guardianPaused -> null
        bootCheck -> if (settings.bootHealthCheckEnabled) "status" else null
        !settings.guardianEnabled -> null
        settings.nodeIntentOperationId.isNotBlank() && settings.desiredNodeState == "stopped" -> "status"
        settings.autoRepairEnabled -> "guardian_check"
        else -> "status"
    }
}
