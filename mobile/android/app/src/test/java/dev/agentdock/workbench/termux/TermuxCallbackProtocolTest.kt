package dev.agentdock.workbench.termux

import org.junit.Assert.*
import org.junit.Test

class TermuxCallbackProtocolTest {
    private fun result(stdout: String = "{}", stderr: String = "") = mutableMapOf<String, Any?>(
        "stdout" to stdout, "stderr" to stderr, "err" to -1, "exitCode" to 0,
        "stdout_original_length" to stdout.length.toString(), "stderr_original_length" to stderr.length.toString()
    )
    @Test fun emptyAcknowledgementsAreNotCompletion() {
        for (fields in listOf(null, emptyMap(), result(""), mapOf("err" to -1))) {
            assertEquals(TermuxCallbackDecision.AwaitCompletion, TermuxCallbackProtocol.decode(fields))
        }
    }
    @Test fun officialStringLengthsAndSuccessSentinelAreDecoded() {
        assertEquals(TermuxCallbackDecision.Result("中😀", "", 0), TermuxCallbackProtocol.decode(result("中😀")))
    }
    @Test fun integerLengthCompatibilityDoesNotChangeSuccessSentinel() {
        val fields = result().apply { put("stdout_original_length", 2); put("stderr_original_length", 0L) }
        assertTrue(TermuxCallbackProtocol.decode(fields) is TermuxCallbackDecision.Result)
        fields["err"] = 0
        assertEquals(TermuxCallbackDecision.ChannelError(0), TermuxCallbackProtocol.decode(fields))
    }
    @Test fun nonzeroExitIsPreservedForReceiptValidation() {
        assertEquals(TermuxCallbackDecision.Result("{}", "", 17), TermuxCallbackProtocol.decode(result().apply { put("exitCode", 17) }))
    }
    @Test fun originalLengthsExposeTruncationEvenForEmptyOutput() {
        val fields = result("").apply { put("stdout_original_length", "200") }
        val decoded = TermuxCallbackProtocol.decode(fields) as TermuxCallbackDecision.Unavailable
        assertTrue(decoded.stdoutTruncated); assertFalse(decoded.stderrTruncated)
    }
    @Test fun stderrTruncationIsNotSuccessfulReceipt() {
        val decoded = TermuxCallbackProtocol.decode(result().apply { put("stderr_original_length", "9") }) as TermuxCallbackDecision.Unavailable
        assertTrue(decoded.stderrTruncated)
    }
    @Test fun rejectsInvalidLengths() {
        for (length in listOf<Any?>(null, -1, "-1", " ", "2.0", "9223372036854775808", true, 2.0, "1")) {
            assertTrue("length=$length", TermuxCallbackProtocol.decode(result().apply { put("stdout_original_length", length) }) is TermuxCallbackDecision.Unavailable)
        }
    }
    @Test fun missingOrWrongTypedCodesAreNotSynthesized() {
        for (key in listOf("err", "exitCode")) {
            assertTrue(TermuxCallbackProtocol.decode(result().apply { remove(key) }) is TermuxCallbackDecision.Unavailable)
            assertTrue(TermuxCallbackProtocol.decode(result().apply { put(key, "0") }) is TermuxCallbackDecision.Unavailable)
        }
    }
    @Test fun missingLengthMetadataSupportsOlderResults() {
        assertTrue(TermuxCallbackProtocol.decode(result().apply { remove("stdout_original_length"); remove("stderr_original_length") }) is TermuxCallbackDecision.Result)
    }
    @Test fun bothStreamsShareTheUtf8Budget() {
        assertTrue(TermuxCallbackProtocol.decode(result("中".repeat(12000), "😀".repeat(9000))) is TermuxCallbackDecision.Unavailable)
    }
    @Test fun callbackFieldsCannotBecomeCommandArguments() {
        assertTrue(TermuxCallbackProtocol.decode(result().apply { put("stdout", listOf("sh", "-c")) }) is TermuxCallbackDecision.Unavailable)
    }
    @Test fun channelErrorsKeepTheirActualCodes() {
        for (code in listOf(0, 2, -7)) assertEquals(TermuxCallbackDecision.ChannelError(code), TermuxCallbackProtocol.decode(result().apply { put("err", code) }))
    }
}
