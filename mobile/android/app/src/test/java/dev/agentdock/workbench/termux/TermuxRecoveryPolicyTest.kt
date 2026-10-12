package dev.agentdock.workbench.termux

import dev.agentdock.workbench.model.ActionOutcome
import dev.agentdock.workbench.model.BridgeOperation
import org.junit.Assert.*
import org.junit.Test

class TermuxRecoveryPolicyTest {
    private val available = ActionOutcome(true, "available", "")
    private fun operation(phase: String, action: String = "probe", data: String = "") = BridgeOperation(
        operationId = "operation", requestId = "request", nonce = "nonce", operation = action,
        phase = phase, createdAtEpochMs = 1_000, resultJson = data
    )

    @Test fun preflightDistinguishesMissingPermissionFromMissingInstallation() {
        for (code in listOf("termux_missing", "permission_missing", "service_missing")) {
            val advice = TermuxRecoveryPolicy.advise(ActionOutcome(false, code, ""), operation("succeeded"), 2_000)
            assertEquals(code, advice.code); assertFalse(advice.allowProbe)
        }
    }
    @Test fun visibleServiceIsNotProofOfWorkingBridge() {
        assertEquals("bridge_unverified", TermuxRecoveryPolicy.advise(available, null, 2_000).code)
        assertEquals("bridge_verified", TermuxRecoveryPolicy.advise(available, operation("succeeded"), 2_000).code)
        assertEquals("bridge_unverified", TermuxRecoveryPolicy.advise(available, operation("succeeded", "install"), 2_000).code)
    }
    @Test fun acceptedRequestsRemainUnconfirmedRatherThanFailed() {
        val pending = operation("running", "install")
        val young = TermuxRecoveryPolicy.advise(available, pending, 15_999)
        assertEquals("awaiting_callback", young.code); assertFalse(young.allowProbe)
        val old = TermuxRecoveryPolicy.advise(available, pending, 16_000)
        assertEquals("callback_unconfirmed", old.code); assertTrue(old.allowProbe)
        assertEquals("running", pending.phase)
        assertEquals("callback_unconfirmed", TermuxRecoveryPolicy.advise(available, operation("unknown"), 30_000).code)
    }
    @Test fun launchDenialDoesNotInventAPermissionProblem() {
        val denied = TermuxRecoveryPolicy.launchRejected(true)
        assertEquals("launch_not_accepted", denied.code)
        assertEquals("permission_missing", TermuxRecoveryPolicy.launchRejected(false).code)
        val cause = IllegalStateException("system restriction")
        val background = TermuxRecoveryPolicy.launchRejected(true, true, cause)
        assertEquals("background_launch_blocked", background.code); assertSame(cause, background.cause)
        val advice = TermuxRecoveryPolicy.advise(available,
            operation("requires_user_action", data = "{\"failure_code\":\"background_launch_blocked\"}"), 2_000)
        assertEquals("background_launch_blocked", advice.code); assertTrue(advice.allowProbe)
    }
    @Test fun malformedAndUnrelatedMetadataDoNotDiagnoseAnOemBlock() {
        for (data in listOf("not json", "{\"other\":\"background_launch_blocked\"}"))
            assertEquals("bridge_unverified", TermuxRecoveryPolicy.advise(available, operation("failed", data = data), 2_000).code)
    }
}
