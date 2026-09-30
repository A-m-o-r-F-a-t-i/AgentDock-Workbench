package dev.agentdock.workbench.termux

import android.content.Context
import android.util.AtomicFile
import dev.agentdock.workbench.model.BridgeOperation
import org.json.JSONObject
import java.io.File
import java.nio.charset.StandardCharsets

class PendingOperationStore(context: Context, private val clock: () -> Long = System::currentTimeMillis) {
    val changes = kotlinx.coroutines.flow.MutableStateFlow(0L)
    private val directory = File(context.filesDir, "operations").apply { mkdirs() }

    @Synchronized
    fun create(value: BridgeOperation) {
        require(validId(value.operationId) && validId(value.requestId))
        check(get(value.operationId) == null) { "Operation ID already exists" }
        expirePending()
        trim(value.operation)
        check(list(MAX_FILES).size < MAX_FILES) { "Unresolved operation capacity reached" }
        if (value.operation !in TermuxResultPolicy.recoveryOperations) {
            check(list(MAX_FILES).count { it.operation !in TermuxResultPolicy.recoveryOperations } < NORMAL_CAPACITY) {
                "Unresolved operation capacity reached; recovery/query slots remain available"
            }
        }
        write(value)
    }

    @Synchronized
    fun get(operationId: String): BridgeOperation? {
        if (!validId(operationId)) return null
        val file = file(operationId)
        if (!file.isFile || file.length() > MAX_FILE_BYTES) return null
        return runCatching { decode(file.readText(StandardCharsets.UTF_8)) }.getOrNull()
    }

    @Synchronized
    fun list(limit: Int = MAX_FILES): List<BridgeOperation> {
        expirePending()
        return records().take(limit.coerceIn(1, MAX_FILES))
    }

    private fun records(): List<BridgeOperation> = directory.listFiles()
        .orEmpty()
        .filter { it.isFile && it.name.endsWith(".json") && it.length() <= MAX_FILE_BYTES }
        .sortedByDescending { it.lastModified() }
        .take(MAX_FILES)
        .mapNotNull { runCatching { decode(it.readText(StandardCharsets.UTF_8)) }.getOrNull() }

    private fun expirePending() {
        val now = clock()
        records().filter { it.phase in TermuxResultPolicy.pendingPhases && now - it.createdAtEpochMs > TermuxResultPolicy.MAX_CALLBACK_AGE_MS }
            .forEach { write(it.copy(phase = "unknown", updatedAtEpochMs = now,
                message = "回执等待超时；业务结果未知，请查询原操作，勿重放写入")) }
    }

    /** Only a validated, explicitly bound query/continuation may settle an old record. */
    @Synchronized
    fun reconcile(request: BridgeOperation, data: JSONObject): Boolean {
        require(request.operation in setOf("operation_query", "resume", "cancel_operation"))
        val query = get(request.operationId) ?: return false
        require(query.requestId == request.requestId && query.nonce == request.nonce && query.phase == "succeeded")
        val target = query.targetOperationId
        require(target.isNotBlank() && data.optString("operation_id") == target)
        val original = get(target) ?: return false
        require(data.optString("operation") == original.operation)
        if (original.phase in setOf("succeeded", "failed", "cancelled")) return false
        val phase = when (data.optString("status")) {
            "succeeded" -> "succeeded"
            "failed", "rolled_back" -> "failed"
            "cancelled" -> "cancelled"
            "requires_user_action" -> "requires_user_action"
            else -> return false
        }
        write(original.copy(phase = phase, updatedAtEpochMs = clock(),
            message = "原操作查询已核对：" + TermuxResultPolicy.safeMessage(data.optString("message", phase)),
            resultJson = BridgeResultData.sanitize(data)))
        return true
    }

    @Synchronized
    fun finish(
        expected: BridgeOperation,
        phase: String,
        message: String,
        exitCode: Int?,
        stdoutTruncated: Boolean,
        stderrTruncated: Boolean,
        resultJson: String = ""
    ): BridgeOperation {
        val current = checkNotNull(get(expected.operationId)) { "Unknown operation" }
        require(current.requestId == expected.requestId && current.nonce == expected.nonce)
        if (current.phase in TermuxResultPolicy.terminalPhases) return current
        if (current.phase == "unknown" && phase in TermuxResultPolicy.pendingPhases) return current
        require(phase == "unknown" || phase in TermuxResultPolicy.pendingPhases || phase in TermuxResultPolicy.terminalPhases)
        val next = current.copy(
            phase = phase,
            message = TermuxResultPolicy.safeMessage(message).take(MAX_MESSAGE_CHARS),
            updatedAtEpochMs = clock(),
            exitCode = exitCode,
            stdoutTruncated = stdoutTruncated,
            stderrTruncated = stderrTruncated,
            resultJson = resultJson
        )
        write(next)
        return next
    }

    private fun write(value: BridgeOperation) {
        val target = AtomicFile(file(value.operationId))
        val bytes = encode(value).toString().toByteArray(StandardCharsets.UTF_8)
        require(bytes.size <= MAX_FILE_BYTES)
        val stream = target.startWrite()
        try {
            stream.write(bytes)
            stream.write('\n'.code)
            target.finishWrite(stream)
            changes.value = changes.value + 1
        } catch (error: Throwable) {
            target.failWrite(stream)
            throw error
        }
    }

    private fun trim(operation: String) {
        val values = records().toMutableList()
        val normal = operation !in TermuxResultPolicy.recoveryOperations
        while (values.size >= MAX_FILES || normal && values.count { it.operation !in TermuxResultPolicy.recoveryOperations } >= NORMAL_CAPACITY) {
            val normalFull = normal && values.count { it.operation !in TermuxResultPolicy.recoveryOperations } >= NORMAL_CAPACITY
            val candidate = values.filter { it.phase in setOf("succeeded", "failed", "cancelled") &&
                (!normalFull || it.operation !in TermuxResultPolicy.recoveryOperations) }.minByOrNull { it.updatedAtEpochMs } ?: break
            AtomicFile(file(candidate.operationId)).delete()
            values.remove(candidate)
        }
    }

    private fun file(operationId: String) = File(directory, "$operationId.json")

    private fun encode(value: BridgeOperation) = JSONObject()
        .put("schema_version", value.schemaVersion)
        .put("operation_id", value.operationId)
        .put("request_id", value.requestId)
        .put("nonce", value.nonce)
        .put("operation", value.operation)
        .put("phase", value.phase)
        .put("message", value.message)
        .put("created_at_epoch_ms", value.createdAtEpochMs)
        .put("updated_at_epoch_ms", value.updatedAtEpochMs)
        .put("exit_code", value.exitCode ?: JSONObject.NULL)
        .put("stdout_truncated", value.stdoutTruncated)
        .put("stderr_truncated", value.stderrTruncated)
        .put("result_data", value.resultJson.takeIf { it.isNotBlank() }?.let(::JSONObject) ?: JSONObject.NULL)
        .put("intent_revision", value.intentRevision)
        .put("target_operation_id", value.targetOperationId)

    private fun decode(value: String): BridgeOperation {
        val json = JSONObject(value)
        require(json.optInt("schema_version") == 1)
        return BridgeOperation(
            operationId = json.getString("operation_id"),
            requestId = json.getString("request_id"),
            nonce = json.getString("nonce"),
            operation = json.getString("operation"),
            phase = json.optString("phase", "queued"),
            message = json.optString("message"),
            createdAtEpochMs = json.getLong("created_at_epoch_ms"),
            updatedAtEpochMs = json.optLong("updated_at_epoch_ms", json.getLong("created_at_epoch_ms")),
            exitCode = if (json.isNull("exit_code")) null else json.getInt("exit_code"),
            stdoutTruncated = json.optBoolean("stdout_truncated"),
            stderrTruncated = json.optBoolean("stderr_truncated"),
            resultJson = json.optJSONObject("result_data")?.toString().orEmpty(),
            intentRevision = json.optLong("intent_revision", -1L),
            targetOperationId = json.optString("target_operation_id", "")
        )
    }

    companion object {
        private const val MAX_FILE_BYTES = 64 * 1024L
        private const val NORMAL_CAPACITY = 128
        private const val RECOVERY_CAPACITY = 16
        private const val MAX_FILES = NORMAL_CAPACITY + RECOVERY_CAPACITY
        private const val MAX_MESSAGE_CHARS = 2048
        private fun validId(value: String) = Regex("^[A-Za-z0-9_-]{1,96}$").matches(value)
    }
}
