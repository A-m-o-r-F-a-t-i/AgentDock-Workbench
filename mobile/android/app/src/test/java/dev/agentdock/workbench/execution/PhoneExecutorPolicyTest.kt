package dev.agentdock.workbench.execution

import org.json.JSONObject
import org.junit.Assert.*
import org.junit.Test

class PhoneExecutorPolicyTest {
    @Test fun executorPinsOnlyLiteralLocalOrigins() {
        assertEquals("127.0.0.1", PhoneExecutorPolicy.localOrigin("http://127.0.0.1:8765").host)
        for (origin in listOf("https://example.com", "http://127.0.0.1:8765/path", "http://user@127.0.0.1", "http://127.0.0.1?token=x")) {
            assertThrows(IllegalArgumentException::class.java) { PhoneExecutorPolicy.localOrigin(origin) }
        }
    }
    private fun instruction() = JSONObject().put("operation_id", "session-" + "a".repeat(24))
        .put("revision", 1).put("action", "observe").put("backend", "termux_host")
    @Test fun instructionNeedsExplicitBackendAndBoundIdentity() {
        assertTrue(PhoneExecutorPolicy.validInstruction(instruction()))
        assertFalse(PhoneExecutorPolicy.validInstruction(instruction().put("operation_id", "../other")))
        assertFalse(PhoneExecutorPolicy.validInstruction(instruction().put("backend", "auto")))
        assertFalse(PhoneExecutorPolicy.validInstruction(instruction().put("revision", 0)))
        assertFalse(PhoneExecutorPolicy.validInstruction(instruction().put("action", "replay")))
    }
}
