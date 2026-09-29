package dev.agentdock.workbench.termux

import android.app.PendingIntent
import android.content.Context
import android.content.Intent
import android.content.pm.PackageManager
import androidx.core.content.ContextCompat
import dev.agentdock.workbench.data.SettingsStore
import dev.agentdock.workbench.model.ActionOutcome
import dev.agentdock.workbench.model.BridgeOperation
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.NonCancellable
import kotlinx.coroutines.withContext
import kotlinx.coroutines.withTimeoutOrNull
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.flow.map
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock
import org.json.JSONObject
import java.net.URI
import java.security.SecureRandom
import java.util.Base64
import java.util.UUID

class TermuxCommandDispatcher(
    private val context: Context,
    private val operations: PendingOperationStore,
    private val pairing: LocalCorePairingManager,
    private val settings: SettingsStore
) {
    private val submissions = Mutex()
    fun availability(): ActionOutcome {
        val packageInstalled = runCatching { context.packageManager.getPackageInfo(TermuxContract.PACKAGE, 0) }.isSuccess
        if (!packageInstalled) return ActionOutcome(false, "termux_missing", "未安装外部 Termux")
        if (ContextCompat.checkSelfPermission(context, TermuxContract.PERMISSION_RUN_COMMAND) != PackageManager.PERMISSION_GRANTED) {
            return ActionOutcome(false, "permission_missing", "请授予 Termux RUN_COMMAND 权限")
        }
        val intent = baseIntent()
        if (context.packageManager.resolveService(intent, PackageManager.MATCH_DEFAULT_ONLY) == null) {
            return ActionOutcome(false, "service_missing", "Termux RUN_COMMAND 服务不可发现；请核对安装来源和版本")
        }
        return ActionOutcome(true, "available", "Termux 服务可发现；桥配置与外部调用开关仍需探测")
    }

    suspend fun dispatch(operation: String, payload: JSONObject = JSONObject()): BridgeOperation {
        val frozen = JSONObject(payload.toString())
        return withContext(Dispatchers.IO) { submissions.withLock { dispatchInternal(operation) { frozen } } }
    }

    suspend fun dispatchLocalPairing(origin: URI): BridgeOperation = withContext(Dispatchers.IO) {
        submissions.withLock { dispatchInternal("pair_local_core") { operationId -> pairing.prepare(operationId, origin) } }
    }

    suspend fun awaitCompletion(operationId: String, timeoutMs: Long = 90_000L): BridgeOperation? = withContext(Dispatchers.IO) {
        require(timeoutMs in 1..120_000L)
        withTimeoutOrNull(timeoutMs) {
            operations.changes.map { operations.get(operationId) }
                .first { it != null && it.phase !in TermuxResultPolicy.pendingPhases }
        }
    }

    private suspend fun dispatchInternal(operation: String, payloadFactory: (String) -> JSONObject): BridgeOperation {
        require(operation in TermuxContract.OPERATIONS) { "Unsupported Termux operation" }
        val availability = availability()
        check(availability.accepted) { availability.message }
        val operationId = "op_${UUID.randomUUID().toString().replace("-", "")}"
        val requestId = "req_${UUID.randomUUID().toString().replace("-", "")}"
        val nonce = nonce()
        val now = System.currentTimeMillis()
        var pending = BridgeOperation(
            operationId = operationId,
            requestId = requestId,
            nonce = nonce,
            operation = operation,
            phase = "queued",
            createdAtEpochMs = now
        )
        var callback: PendingIntent? = null
        var stored = false
        var dispatched = false
        val before = settings.current()
        try {
            val payloadValue = payloadFactory(operationId)
                .put("schema_version", 1)
                .put("operation_id", operationId)
                .put("request_id", requestId)
                .put("nonce", nonce)
                .put("operation", operation)
            val payloadText = payloadValue.toString()
            require(payloadText.toByteArray().size <= TermuxContract.MAX_PAYLOAD_BYTES) { "Termux payload too large" }
            require(!TermuxResultPolicy.containsSecretFields(payloadValue)) { "Credentials cannot be sent through RUN_COMMAND" }
            pending = pending.copy(
                intentRevision = settings.beginNodeOperation(operation, operationId, payloadValue),
                targetOperationId = payloadValue.optString("target_operation_id", "")
            )
            operations.create(pending)
            stored = true

            callback = TermuxResultCallbacks.create(context, pending)

            val intent = baseIntent()
                .putExtra(TermuxContract.EXTRA_COMMAND_PATH, TermuxContract.COMMAND)
                .putExtra(TermuxContract.EXTRA_ARGUMENTS, arrayOf(operation, operationId, requestId, nonce, "-"))
                .putExtra(TermuxContract.EXTRA_STDIN, payloadText)
                .putExtra(TermuxContract.EXTRA_WORKDIR, TermuxContract.WORKDIR)
                .putExtra(TermuxContract.EXTRA_BACKGROUND, true)
                .putExtra(TermuxContract.EXTRA_PENDING_INTENT, callback)
            context.startService(intent)
            dispatched = true
            operations.finish(pending, "running", "已发出 Termux 请求，等待结构化回执确认", null, false, false)
            return operations.get(operationId) ?: pending
        } catch (error: Exception) {
            if (!dispatched) {
                callback?.cancel()
                if (operation == "pair_local_core") pairing.discard(operationId)
                withContext(NonCancellable) { settings.rejectUnsentNodeOperation(pending, before) }
            }
            if (stored) runCatching {
                operations.finish(
                    pending,
                    if (dispatched) "unknown" else "failed",
                    if (dispatched) "请求已发送但本地记录未完成，请核对原操作" else "无法启动 Termux RUN_COMMAND（${error.javaClass.simpleName}）",
                    null,
                    false,
                    false
                )
            }
            throw error
        }
    }

    private fun baseIntent() = Intent(TermuxContract.ACTION_RUN_COMMAND).apply {
        component = TermuxContract.RUN_COMMAND_COMPONENT
        setPackage(TermuxContract.PACKAGE)
    }

    private fun nonce(): String {
        val bytes = ByteArray(24)
        random.nextBytes(bytes)
        return Base64.getUrlEncoder().withoutPadding().encodeToString(bytes)
    }

    companion object {
        private val random = SecureRandom()
    }
}
