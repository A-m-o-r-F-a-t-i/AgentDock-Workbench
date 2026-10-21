package dev.agentdock.workbench.lifecycle

import dev.agentdock.workbench.model.BridgeOperation

/** Maps a validated bridge receipt to WorkManager semantics without collapsing failure into success. */
enum class GuardianWorkOutcome { Success, Retry, Failure }

object GuardianWorkResultPolicy {
    fun classify(receipt: BridgeOperation?): GuardianWorkOutcome = when (receipt?.phase) {
        null, "queued", "running", "unknown" -> GuardianWorkOutcome.Retry
        "succeeded", "cancelled", "pending_manifest", "requires_user_action" -> GuardianWorkOutcome.Success
        "failed" -> GuardianWorkOutcome.Failure
        else -> GuardianWorkOutcome.Retry
    }
}
