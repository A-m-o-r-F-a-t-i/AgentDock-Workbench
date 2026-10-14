package dev.agentdock.workbench.ui

import dev.agentdock.workbench.model.BridgeOperation
import dev.agentdock.workbench.model.WorkbenchScreen
import org.json.JSONObject
import java.net.URI

/** Converts Core transport facts and the latest local-node observation into one actionable diagnosis. */
internal data class CoreFailureAdvice(
    val title: String,
    val detail: String,
    val actionLabel: String = "",
    val destination: WorkbenchScreen? = null
)

internal object CoreFailurePolicy {
    private val deploymentOperations = setOf("install", "update", "rollback")
    private val statusOperations = setOf("probe", "status")

    fun advise(
        errors: List<String>,
        operations: List<BridgeOperation>,
        endpoint: String,
        desiredState: String
    ): CoreFailureAdvice {
        val failure = errors.joinToString("\n").lowercase()
        val statusRecord = operations
            .asSequence()
            .filter { it.operation in statusOperations && it.phase == "succeeded" && it.resultJson.isNotBlank() }
            .mapNotNull { operation ->
                runCatching { operation to JSONObject(operation.resultJson) }.getOrNull()
            }
            .maxByOrNull { it.first.updatedAtEpochMs }
        val status = statusRecord?.second
        val deploymentAfterStatus = statusRecord?.first?.updatedAtEpochMs?.let { observedAt ->
            operations.any {
                it.operation in deploymentOperations && it.phase == "succeeded" && it.updatedAtEpochMs > observedAt
            }
        } ?: false
        val loopback = runCatching { URI(endpoint).host?.lowercase() }
            .getOrNull() in setOf("127.0.0.1", "localhost", "::1")

        if (loopback && status?.has("version") == true && status.optString("version").isBlank() && !deploymentAfterStatus) {
            val bundledCoreReady = status.optBoolean("bundled_core_ready")
            val preparation = if (bundledCoreReady) {
                "前往“安装与更新”，部署 APK 内置 Core 并启动节点。"
            } else {
                "先从当前 APK 重新导出并运行完整 Termux 桥包，再部署其中的内置 Core。"
            }
            return CoreFailureAdvice(
                title = "本机 Core 尚未安装",
                detail = "Termux 桥可以返回节点状态，但手机上没有受管 Core。$preparation",
                actionLabel = "前往安装与更新",
                destination = WorkbenchScreen.InstallUpdate
            )
        }

        val version = status?.optString("version").orEmpty()
        val observedDesired = status?.optString("desired_state").orEmpty().ifBlank { desiredState }
        if (loopback && version.isNotBlank() && observedDesired == "stopped") {
            return CoreFailureAdvice(
                title = "本机 Core 已停止",
                detail = "手机已安装 Core $version，当前运行意图为停止。启动节点并通过健康检查后再读取数据。",
                actionLabel = "前往安装与更新",
                destination = WorkbenchScreen.InstallUpdate
            )
        }

        if (failure.contains("unauthorized") || failure.contains("http 401") || failure.contains("http_401")) {
            return CoreFailureAdvice(
                title = "Core 凭据未授权",
                detail = "已连接到 Core，但当前手机凭据未被接受。请重新配对目标 Core；切换 Core 地址后，旧凭据不会发送给新地址。",
                actionLabel = "前往 Core 与连接",
                destination = WorkbenchScreen.CoreConnections
            )
        }

        val connectionFailure = listOf(
            "failed to connect", "connection refused", "connectexception", "econnrefused", "network is unreachable"
        ).any(failure::contains)
        if (connectionFailure && loopback) {
            val runningIntent = observedDesired == "running"
            return CoreFailureAdvice(
                title = if (runningIntent) "本机 Core 启动未完成" else "无法连接本机 Core",
                detail = if (runningIntent) {
                    "运行意图为启动，但手机 127.0.0.1:8765 尚无可用 Core。请在安装页查询节点状态、日志和恢复记录。"
                } else {
                    "手机本机端口尚无可用 Core。请在安装页确认 Core 已安装并启动。"
                },
                actionLabel = "前往安装与更新",
                destination = WorkbenchScreen.InstallUpdate
            )
        }

        if (connectionFailure) {
            return CoreFailureAdvice(
                title = "无法连接 Core",
                detail = "当前 Core 地址不可达。请核对地址、隧道和目标节点运行状态。",
                actionLabel = "前往 Core 与连接",
                destination = WorkbenchScreen.CoreConnections
            )
        }

        return CoreFailureAdvice(
            title = "Core 数据读取失败",
            detail = "Core 已返回错误或数据格式未通过验证。保留原始错误并重新读取；问题持续时从日志与诊断页导出证据。"
        )
    }
}
