package dev.agentdock.workbench.termux

/** Transport state is separate from the outcome of the operation inside Termux. */
sealed interface TermuxCallbackDecision {
    data object AwaitCompletion : TermuxCallbackDecision
    data class Unavailable(
        val reason: String,
        val stdoutTruncated: Boolean = false,
        val stderrTruncated: Boolean = false
    ) : TermuxCallbackDecision
    data class ChannelError(val code: Int) : TermuxCallbackDecision
    data class Result(val stdout: String, val stderr: String, val exitCode: Int) : TermuxCallbackDecision
}

/** Android-free decoder; official Termux lengths are decimal String UTF-16 counts. */
object TermuxCallbackProtocol {
    const val RESULT_OK = -1
    private const val MAX_BYTES = 64 * 1024
    val resultKeys = setOf("stdout", "stderr", "stdout_original_length", "stderr_original_length", "exitCode", "err", "errmsg")

    fun decode(fields: Map<String, Any?>?): TermuxCallbackDecision {
        if (fields.isNullOrEmpty()) return TermuxCallbackDecision.AwaitCompletion
        val stdoutValue = fields["stdout"]
        val stderrValue = fields["stderr"]
        if (stdoutValue != null && stdoutValue !is String || stderrValue != null && stderrValue !is String) {
            return TermuxCallbackDecision.Unavailable("回执输出字段类型无效")
        }
        val stdout = stdoutValue as? String ?: ""
        val stderr = stderrValue as? String ?: ""
        val outLength = length(fields, "stdout_original_length", stdout.length)
        val errLength = length(fields, "stderr_original_length", stderr.length)
        if (outLength == null || errLength == null || outLength < stdout.length || errLength < stderr.length) {
            return TermuxCallbackDecision.Unavailable("回执原始长度字段无效")
        }
        val outTruncated = outLength > stdout.length
        val errTruncated = errLength > stderr.length
        if (outTruncated || errTruncated) return TermuxCallbackDecision.Unavailable(
            "回执输出被截断", outTruncated, errTruncated
        )
        if (stdout.length > MAX_BYTES || stderr.length > MAX_BYTES ||
            stdout.toByteArray(Charsets.UTF_8).size.toLong() + stderr.toByteArray(Charsets.UTF_8).size > MAX_BYTES) {
            return TermuxCallbackDecision.Unavailable("回执超过 UTF-8 大小限制")
        }
        val code = fields["err"] as? Int
        val exit = fields["exitCode"] as? Int
        if (fields.containsKey("err") && code == null || fields.containsKey("exitCode") && exit == null) {
            return TermuxCallbackDecision.Unavailable("回执返回码类型无效")
        }
        if (code != null && code != RESULT_OK) return TermuxCallbackDecision.ChannelError(code)
        // Fixed Workbench operations always return bound JSON. An empty successful
        // start acknowledgement cannot establish their completion, even with exit=0.
        if (stdout.isEmpty() && stderr.isEmpty() && (exit == null || exit == 0) &&
            fields["errmsg"].let { it == null || it == "" }) {
            return TermuxCallbackDecision.AwaitCompletion
        }
        if (code == null || exit == null) return TermuxCallbackDecision.Unavailable("回执缺少返回码")
        return TermuxCallbackDecision.Result(stdout, stderr, exit)
    }

    private fun length(fields: Map<String, Any?>, key: String, received: Int): Long? {
        if (!fields.containsKey(key)) return received.toLong()
        val value = when (val raw = fields[key]) {
            is String -> if (raw.length in 1..19 && raw.all { it in '0'..'9' }) raw.toLongOrNull() else null
            is Int -> raw.toLong() // Compatibility with previously used integer fixtures/variants.
            is Long -> raw
            else -> null
        }
        return value?.takeIf { it >= 0 }
    }
}
