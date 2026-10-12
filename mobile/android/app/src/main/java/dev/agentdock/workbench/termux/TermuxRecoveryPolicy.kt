package dev.agentdock.workbench.termux

import dev.agentdock.workbench.model.ActionOutcome
import dev.agentdock.workbench.model.BridgeOperation
import org.json.JSONObject

/** Observations, not retries: lack of a receipt never establishes command failure. */
data class TermuxRecoveryAdvice(val code: String, val title: String, val detail: String, val allowProbe: Boolean)

class TermuxLaunchFailure(val code: String, message: String, cause: Throwable? = null) :
    IllegalStateException(message, cause)

object TermuxRecoveryPolicy {
    const val SLOW_RESPONSE_MS = 15_000L

    fun launchRejected(permissionGranted: Boolean, background: Boolean = false, cause: Throwable? = null): TermuxLaunchFailure = when {
        !permissionGranted -> TermuxLaunchFailure("permission_missing", "Termux 调用权限未获授权，请先授予权限。", cause)
        background -> TermuxLaunchFailure("background_launch_blocked", "系统限制了 Termux 后台启动。请先打开 Termux，返回后重新检测。", cause)
        else -> TermuxLaunchFailure("launch_not_accepted", "系统未接受 Termux 启动请求。请先打开 Termux，返回后重新检测。", cause)
    }

    fun advise(availability: ActionOutcome, latest: BridgeOperation?, nowEpochMs: Long): TermuxRecoveryAdvice {
        if (!availability.accepted) return when (availability.code) {
            "termux_missing" -> TermuxRecoveryAdvice("termux_missing", "尚未安装 Termux", "安装官方 Termux 并打开一次，再回到这里准备运行环境。", false)
            "permission_missing" -> TermuxRecoveryAdvice("permission_missing", "需要调用权限", "点击“授予 RUN_COMMAND”，允许 AgentDock 调用 Termux。", false)
            else -> TermuxRecoveryAdvice("service_missing", "Termux 服务不可用", "请打开 Termux 完成首次启动，并核对安装来源。系统已禁用的应用需要先恢复。", false)
        }
        val failure = latest?.resultJson?.let { runCatching { JSONObject(it).optString("failure_code") }.getOrNull() }.orEmpty()
        if (failure in setOf("launch_not_accepted", "background_launch_blocked"))
            return TermuxRecoveryAdvice(failure, "需要先打开 Termux", "请打开 Termux，返回后点击“重新检测”。手机的应用启动管理可能限制后台启动；可在 Termux 的系统设置中允许后台活动。", true)
        if (latest?.phase in TermuxResultPolicy.pendingPhases) {
            if (nowEpochMs - latest!!.createdAtEpochMs < SLOW_RESPONSE_MS)
                return TermuxRecoveryAdvice("awaiting_callback", "等待 Termux 响应", "请求已发出，尚未收到执行回执。", false)
            return TermuxRecoveryAdvice("callback_unconfirmed", "尚未收到执行回执", "操作可能仍在进行，结果暂不确定。先打开 Termux，返回后只读检测；安装或写入操作应查询原记录，不要重复执行。", true)
        }
        if (latest?.phase == "unknown")
            return TermuxRecoveryAdvice("callback_unconfirmed", "执行结果待确认", "先打开 Termux 并重新检测连接，再查询原操作记录。未确认的安装和写入不会自动重放。", true)
        if (latest?.operation in setOf("probe", "status") && latest?.phase == "succeeded")
            return TermuxRecoveryAdvice("bridge_verified", "Termux 已响应", "已收到有效桥接回执。节点是否运行以本次查询结果为准。", true)
        return TermuxRecoveryAdvice("bridge_unverified", "检查运行环境", "服务可见，桥接脚本和外部调用开关尚未验证。首次使用请在 Termux 执行导出包中的 bootstrap，再点击检测。", true)
    }
}
