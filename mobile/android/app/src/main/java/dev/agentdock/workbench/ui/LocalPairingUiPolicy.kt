package dev.agentdock.workbench.ui

import dev.agentdock.workbench.model.BridgeOperation

internal data class LocalPairingUiDecision(
    val refreshCore: Boolean,
    val message: String
)

/** Maps a bound Termux pairing receipt to the next UI action without inventing success. */
internal object LocalPairingUiPolicy {
    fun evaluate(
        operationId: String,
        completion: BridgeOperation?,
        credentialAvailable: Boolean
    ): LocalPairingUiDecision {
        require(operationId.isNotBlank())
        if (completion == null) {
            return LocalPairingUiDecision(
                refreshCore = false,
                message = "本机配对 $operationId 的回执尚未确认；请查询原操作，勿重复提交。"
            )
        }
        require(completion.operationId == operationId)
        if (completion.phase == "succeeded") {
            return if (credentialAvailable) {
                LocalPairingUiDecision(
                    refreshCore = true,
                    message = "本机 Core 配对完成；已重新加载当前 Origin 的 Keystore 凭据。"
                )
            } else {
                LocalPairingUiDecision(
                    refreshCore = false,
                    message = "本机配对回执为成功，但当前 Origin 的凭据未写入；已保留操作回执供诊断。"
                )
            }
        }
        val detail = completion.message.trim().ifBlank { completion.phase }
        return LocalPairingUiDecision(
            refreshCore = false,
            message = "本机 Core 配对未完成（${completion.phase}）：$detail"
        )
    }
}
