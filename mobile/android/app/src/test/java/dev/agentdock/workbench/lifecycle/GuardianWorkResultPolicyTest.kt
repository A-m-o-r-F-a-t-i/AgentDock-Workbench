package dev.agentdock.workbench.lifecycle

import dev.agentdock.workbench.model.BridgeOperation
import org.junit.Assert.assertEquals
import org.junit.Test

class GuardianWorkResultPolicyTest {
    @Test fun onlySuccessfulOrUserBlockedTerminalReceiptsCompleteTheWorker() {
        for (phase in listOf("succeeded", "cancelled", "pending_manifest", "requires_user_action")) {
            assertEquals(GuardianWorkOutcome.Success, GuardianWorkResultPolicy.classify(receipt(phase)))
        }
    }

    @Test fun transportUncertaintyAndPendingReceiptsRetry() {
        assertEquals(GuardianWorkOutcome.Retry, GuardianWorkResultPolicy.classify(null))
        for (phase in listOf("queued", "running", "unknown", "unexpected")) {
            assertEquals(GuardianWorkOutcome.Retry, GuardianWorkResultPolicy.classify(receipt(phase)))
        }
    }

    @Test fun explicitBridgeFailureIsNotReportedAsSuccess() {
        assertEquals(GuardianWorkOutcome.Failure, GuardianWorkResultPolicy.classify(receipt("failed")))
    }

    private fun receipt(phase: String) = BridgeOperation(
        operationId = "op_test",
        requestId = "req_test",
        nonce = "nonce_test_1234567890",
        operation = "guardian_check",
        phase = phase,
        createdAtEpochMs = 1L
    )
}
