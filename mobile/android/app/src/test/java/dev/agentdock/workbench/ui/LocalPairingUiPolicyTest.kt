package dev.agentdock.workbench.ui

import dev.agentdock.workbench.model.BridgeOperation
import org.junit.Assert.assertFalse
import org.junit.Assert.assertThrows
import org.junit.Assert.assertTrue
import org.junit.Test

class LocalPairingUiPolicyTest {
    @Test fun successfulReceiptWithStoredCredentialRefreshesCore() {
        val decision = LocalPairingUiPolicy.evaluate(OPERATION_ID, completion("succeeded"), credentialAvailable = true)

        assertTrue(decision.refreshCore)
        assertTrue(decision.message.contains("配对完成"))
        assertTrue(decision.message.contains("重新加载"))
    }

    @Test fun successfulReceiptWithoutStoredCredentialDoesNotClaimCompletion() {
        val decision = LocalPairingUiPolicy.evaluate(OPERATION_ID, completion("succeeded"), credentialAvailable = false)

        assertFalse(decision.refreshCore)
        assertTrue(decision.message.contains("凭据未写入"))
        assertFalse(decision.message.contains("配对完成"))
    }

    @Test fun failedReceiptKeepsDiagnosticAndDoesNotRefresh() {
        val decision = LocalPairingUiPolicy.evaluate(
            OPERATION_ID,
            completion("failed", "密文验证失败"),
            credentialAvailable = false
        )

        assertFalse(decision.refreshCore)
        assertTrue(decision.message.contains("failed"))
        assertTrue(decision.message.contains("密文验证失败"))
    }

    @Test fun missingReceiptKeepsOriginalOperationQueryable() {
        val decision = LocalPairingUiPolicy.evaluate(OPERATION_ID, completion = null, credentialAvailable = false)

        assertFalse(decision.refreshCore)
        assertTrue(decision.message.contains(OPERATION_ID))
        assertTrue(decision.message.contains("勿重复提交"))
    }

    @Test fun mismatchedReceiptCannotSettleAnotherPairing() {
        assertThrows(IllegalArgumentException::class.java) {
            LocalPairingUiPolicy.evaluate(OPERATION_ID, completion("succeeded").copy(operationId = "op_other"), true)
        }
    }

    private fun completion(phase: String, message: String = "") = BridgeOperation(
        operationId = OPERATION_ID,
        requestId = "req_pair_refresh_123456",
        nonce = "abcdefghijklmnopqrstuvwxyzABCDE123456",
        operation = "pair_local_core",
        phase = phase,
        message = message,
        createdAtEpochMs = 1L,
        updatedAtEpochMs = 2L,
        exitCode = if (phase == "succeeded") 0 else 1
    )

    companion object {
        private const val OPERATION_ID = "op_pair_refresh_123456"
    }
}
