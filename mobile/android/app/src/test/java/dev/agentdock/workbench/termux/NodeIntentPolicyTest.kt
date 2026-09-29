package dev.agentdock.workbench.termux

import dev.agentdock.workbench.model.BridgeOperation
import dev.agentdock.workbench.model.WorkbenchSettings
import org.json.JSONObject
import org.junit.Assert.*
import org.junit.Test

class NodeIntentPolicyTest {
    private fun receipt(id: String, revision: Long, operation: String = "install", target: String = "") = BridgeOperation(
        operationId = id, requestId = "req_$id", nonce = "fixture", operation = operation,
        createdAtEpochMs = 1L, intentRevision = revision, targetOperationId = target
    )

    @Test fun explicitDeploymentStartAlwaysRegistersIntent() {
        for (operation in listOf("install", "update", "rollback")) {
            val state = NodeIntentPolicy.begin(WorkbenchSettings(), operation, "op", JSONObject().put("start_after_install", true))
            assertEquals("running", state.desiredNodeState)
            assertEquals(1L, state.nodeIntentRevision)
            assertEquals("op", state.nodeIntentOperationId)
        }
    }

    @Test fun resumeConfirmationUsesTheSameIntentBoundary() {
        val state = NodeIntentPolicy.begin(WorkbenchSettings(), "resume", "resume", JSONObject().put("confirm_start", true))
        assertEquals("running", state.desiredNodeState)
        assertEquals("resume", state.nodeIntentOperationId)
    }

    @Test fun ReadOnlyAndUnconfirmedOperationsDoNotAuthorizeStart() {
        val initial = WorkbenchSettings()
        for (operation in listOf("status", "probe", "repair", "guardian_check", "install", "rollback", "resume")) {
            assertEquals(initial, NodeIntentPolicy.begin(initial, operation, "op", JSONObject()))
        }
    }

    @Test fun lateStartReceiptCannotReverseLaterStop() {
        val start = NodeIntentPolicy.begin(WorkbenchSettings(), "start", "start", JSONObject())
        val stop = NodeIntentPolicy.begin(start, "stop", "stop", JSONObject())
        val observed = NodeIntentPolicy.observe(stop, receipt("start", start.nodeIntentRevision), JSONObject().put("desired_state", "running"))
        assertEquals(stop, observed)
    }

    @Test fun statusIssuedWhileStopPendingCannotReverseIt() {
        val stop = NodeIntentPolicy.begin(WorkbenchSettings(), "stop", "stop", JSONObject())
        assertEquals(stop, NodeIntentPolicy.observe(stop, receipt("status", stop.nodeIntentRevision, "status"), JSONObject().put("desired_state", "running")))
    }

    @Test fun matchingReceiptSettlesLatestIntent() {
        val start = NodeIntentPolicy.begin(WorkbenchSettings(), "start", "start", JSONObject())
        val observed = NodeIntentPolicy.observe(start, receipt("start", start.nodeIntentRevision), JSONObject().put("desired_state", "running"))
        assertEquals("running", observed.desiredNodeState)
        assertEquals("", observed.nodeIntentOperationId)
        assertEquals(start.nodeIntentRevision, observed.nodeIntentRevision)
    }

    @Test fun onlyBoundTerminalQuerySettlesPendingIntent() {
        val stop = NodeIntentPolicy.begin(WorkbenchSettings(), "stop", "stop", JSONObject())
        val request = receipt("query", stop.nodeIntentRevision, "operation_query", "stop")
        val data = JSONObject().put("operation_id", "stop").put("status", "running").put("desired_state", "running")
        assertEquals(stop, NodeIntentPolicy.observe(stop, request, data))
        data.put("status", "succeeded").put("desired_state", "stopped")
        assertEquals("", NodeIntentPolicy.observe(stop, request, data).nodeIntentOperationId)
        assertEquals(stop, NodeIntentPolicy.observe(stop, request.copy(targetOperationId = "other"), data))
    }

    @Test fun adoptionFencesOldNodeReplies() {
        val before = WorkbenchSettings()
        val adopted = NodeIntentPolicy.begin(before, "adopt", "adopt", JSONObject())
        assertEquals(adopted, NodeIntentPolicy.observe(adopted, receipt("old", 0L, "status"), JSONObject().put("desired_state", "running")))
        val observed = NodeIntentPolicy.observe(adopted, receipt("adopt", adopted.nodeIntentRevision, "adopt"), JSONObject().put("desired_state", "running"))
        assertEquals("running", observed.desiredNodeState)
        assertEquals("", observed.nodeIntentOperationId)
    }

    @Test fun legacyReceiptDoesNotChangeIntent() {
        val current = WorkbenchSettings()
        assertEquals(current, NodeIntentPolicy.observe(current, receipt("old", -1L), JSONObject().put("desired_state", "running")))
    }
}
