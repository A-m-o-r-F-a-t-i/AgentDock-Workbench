package dev.agentdock.workbench.termux

import dev.agentdock.workbench.model.BridgeOperation
import dev.agentdock.workbench.model.WorkbenchSettings
import org.json.JSONObject

/** Client intent ordering only. The Termux node remains the runtime authority. */
object NodeIntentPolicy {
    fun requestedState(operation: String, payload: JSONObject): String? = when {
        operation in setOf("start", "restart") -> "running"
        operation == "stop" -> "stopped"
        operation in setOf("install", "update", "rollback") && payload.opt("start_after_install") == true -> "running"
        operation == "resume" && payload.opt("confirm_start") == true -> "running"
        else -> null
    }

    fun begin(current: WorkbenchSettings, operation: String, operationId: String, payload: JSONObject): WorkbenchSettings {
        val desired = requestedState(operation, payload)
        if (desired == null && operation != "adopt") return current
        return current.copy(
            desiredNodeState = desired ?: current.desiredNodeState,
            nodeIntentRevision = Math.addExact(current.nodeIntentRevision, 1L),
            nodeIntentOperationId = operationId
        )
    }

    fun observe(current: WorkbenchSettings, request: BridgeOperation, data: JSONObject): WorkbenchSettings {
        if (request.intentRevision < 0 || request.intentRevision != current.nodeIntentRevision) return current
        val pending = current.nodeIntentOperationId
        val verifiedQuery = request.operation in setOf("operation_query", "resume", "cancel_operation") && request.targetOperationId == pending &&
            data.optString("operation_id") == pending &&
            data.optString("status") in setOf("succeeded", "failed", "rolled_back", "cancelled")
        if (pending.isNotBlank() && pending != request.operationId && !verifiedQuery) return current
        val desired = data.optString("desired_state")
        if (desired !in setOf("running", "stopped")) return current
        return current.copy(desiredNodeState = desired, nodeIntentOperationId = "")
    }
}
