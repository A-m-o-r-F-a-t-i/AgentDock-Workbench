package dev.agentdock.workbench.termux

import android.app.Service
import android.content.Intent
import android.os.IBinder
import dev.agentdock.workbench.WorkbenchApplication
import dev.agentdock.workbench.model.BridgeOperation
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.cancel
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import kotlinx.coroutines.channels.Channel
import org.json.JSONObject

class TermuxResultService : Service() {
    private val scope = CoroutineScope(SupervisorJob() + Dispatchers.IO)
    private val callbacks = Channel<Intent>(capacity = 144)
    // Accessed only on Main, including completion after IO processing.
    private var queued = 0
    private var latestStartId = 0

    override fun onCreate() {
        super.onCreate()
        scope.launch {
            for (intent in callbacks) {
                try {
                    handle(intent)
                } catch (error: CancellationException) {
                    throw error
                } catch (_: Exception) {
                    // No invented success: an unreadable callback remains available
                    // for original-ID reconciliation after the transport times out.
                } finally {
                    withContext(Dispatchers.Main) {
                        queued--
                        if (queued == 0) stopSelf(latestStartId)
                    }
                }
            }
        }
    }

    override fun onBind(intent: Intent?): IBinder? = null

    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        latestStartId = startId
        if (intent != null) {
            queued++
            if (callbacks.trySend(Intent(intent)).isFailure) queued--
        }
        if (queued == 0) stopSelf(startId)
        return START_NOT_STICKY
    }

    override fun onDestroy() {
        callbacks.close()
        scope.cancel()
        super.onDestroy()
    }

    private suspend fun handle(intent: Intent) {
        val operationId = intent.getStringExtra(EXTRA_OPERATION_ID).orEmpty()
        val requestId = intent.getStringExtra(EXTRA_REQUEST_ID).orEmpty()
        val nonce = intent.getStringExtra(EXTRA_NONCE).orEmpty()
        val graph = (application as WorkbenchApplication).graph
        val store = graph.operations
        val expected = store.get(operationId) ?: return
        if (!TermuxResultPolicy.mayComplete(expected, System.currentTimeMillis())) return
        if (expected.requestId != requestId || expected.nonce != nonce || intent.action != TermuxContract.CALLBACK_ACTION_PREFIX + requestId) return

        val bundle = intent.getBundleExtra(TermuxContract.EXTRA_RESULT_BUNDLE) ?: intent.extras
        @Suppress("DEPRECATION")
        val fields = bundle?.let { values ->
            TermuxCallbackProtocol.resultKeys.filter { values.containsKey(it) }.associateWith { values.get(it) }
        }
        val decoded = TermuxCallbackProtocol.decode(fields)
        if (decoded == TermuxCallbackDecision.AwaitCompletion) return
        if (decoded is TermuxCallbackDecision.Unavailable || decoded is TermuxCallbackDecision.ChannelError) {
            val message = when (decoded) {
                is TermuxCallbackDecision.Unavailable -> decoded.reason
                is TermuxCallbackDecision.ChannelError -> "Termux 执行通道错误（${decoded.code}）"
                else -> error("unreachable")
            }
            store.finish(expected, "unknown", "$message；业务结果未知，请查询原操作，勿重放写入", null,
                (decoded as? TermuxCallbackDecision.Unavailable)?.stdoutTruncated == true,
                (decoded as? TermuxCallbackDecision.Unavailable)?.stderrTruncated == true)
            return
        }
        decoded as TermuxCallbackDecision.Result
        val stdout = decoded.stdout
        val exitCode = decoded.exitCode
        var result = TermuxResultValidator.validate(expected, stdout, decoded.stderr, exitCode, TermuxCallbackProtocol.RESULT_OK, "")
        if (!result.receiptValid) {
            store.finish(expected, "unknown", result.message + "；业务结果未知，请查询原操作", null, false, false)
            return
        }
        if (expected.operation == "pair_local_core") {
            result = if (result.phase == "succeeded") {
                runCatching {
                    val data = JSONObject(stdout).getJSONObject("data")
                    val origin = graph.localPairing.consume(expected.operationId, data)
                    ValidatedTermuxResult(
                        "succeeded",
                        "本机 Core 凭据已通过一次性公钥解密并绑定到 $origin",
                        JSONObject().put("paired", true).put("origin", origin.toString()).toString()
                    )
                }.getOrElse { error ->
                    graph.localPairing.discard(expected.operationId)
                    ValidatedTermuxResult("failed", "本机配对密文验证或解密失败（${error.javaClass.simpleName}）")
                }
            } else {
                graph.localPairing.discard(expected.operationId)
                result
            }
        }
        store.finish(
            expected = expected,
            phase = result.phase,
            message = result.message,
            exitCode = exitCode,
            stdoutTruncated = false,
            stderrTruncated = false,
            resultJson = result.dataJson
        )
        if (result.phase == "succeeded" && result.dataJson.isNotBlank()) {
            val data = JSONObject(result.dataJson)
            if (expected.operation in setOf("operation_query", "resume", "cancel_operation")) {
                store.reconcile(expected, data)
            }
            graph.settings.observeNodeOperation(expected, data)
        }
    }

    companion object {
        const val EXTRA_OPERATION_ID = "operation_id"
        const val EXTRA_REQUEST_ID = "request_id"
        const val EXTRA_NONCE = "nonce"
    }
}

data class ValidatedTermuxResult(
    val phase: String,
    val message: String,
    val dataJson: String = "",
    val receiptValid: Boolean = true
)

object TermuxResultValidator {
    private fun rejected(message: String) = ValidatedTermuxResult("failed", message, receiptValid = false)

    fun validate(
        expected: BridgeOperation,
        stdout: String,
        stderr: String,
        exitCode: Int?,
        pluginError: Int?,
        @Suppress("UNUSED_PARAMETER") pluginMessage: String,
        nowEpochMs: Long = System.currentTimeMillis()
    ): ValidatedTermuxResult {
        if (!TermuxResultPolicy.mayComplete(expected, nowEpochMs)) {
            return rejected("回执已过期或操作已有终态，请查询原操作记录")
        }
        if (pluginError != TermuxCallbackProtocol.RESULT_OK) return rejected("Termux 执行通道返回码无效（${pluginError ?: "missing"}）")
        if (exitCode == null) return rejected("Termux 回执缺少命令退出码")
        if (stdout.length > TermuxContract.MAX_RESULT_CHARS || stderr.length > TermuxContract.MAX_RESULT_CHARS ||
            stdout.toByteArray(Charsets.UTF_8).size.toLong() + stderr.toByteArray(Charsets.UTF_8).size > TermuxContract.MAX_RESULT_CHARS) {
            return rejected("Termux 回执超过大小限制")
        }
        val json = runCatching { JSONObject(stdout) }.getOrElse {
            return rejected("Termux 未返回有效 JSON；原始输出不写入操作摘要")
        }
        if (json.opt("schema_version") != 1 ||
            json.optString("operation_id") != expected.operationId ||
            json.optString("request_id") != expected.requestId ||
            json.optString("nonce") != expected.nonce ||
            json.optString("operation") != expected.operation
        ) return rejected("Termux 回执与请求绑定不一致")

        if (TermuxResultPolicy.containsSecretFields(json)) {
            return rejected("旧桥返回了凭据字段，已拒绝导入；请更新桥并使用管理连接配对")
        }
        val dataJson = BridgeResultData.sanitize(json.optJSONObject("data"))
        if (exitCode != 0) return ValidatedTermuxResult("failed",
            TermuxResultPolicy.safeMessage(json.optString("message")).ifBlank { "Termux 执行失败（exit ${exitCode ?: "unknown"}）" }, dataJson)
        val status = json.optString("status")
        val message = TermuxResultPolicy.safeMessage(json.optString("message")).ifBlank { status }
        return when {
            status == "pending_manifest" -> ValidatedTermuxResult("pending_manifest", message, dataJson)
            status == "requires_user_action" -> ValidatedTermuxResult("requires_user_action", message, dataJson)
            status in setOf("ok", "healthy", "running", "stopped", "adopted", "installed", "updated", "rolled_back") && exitCode == 0 ->
                ValidatedTermuxResult("succeeded", message, dataJson)
            else -> rejected("Termux 回执状态无法识别，请查询原操作")
        }
    }
}
