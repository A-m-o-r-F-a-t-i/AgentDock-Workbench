package dev.agentdock.workbench.shizuku

import org.json.JSONObject
import org.junit.Assert.*
import org.junit.Test

class ShizukuProtocolTest {
    private fun spec() = JSONObject().put("backend", "android_shizuku").put("command", "id").put("workdir", "/")
        .put("timeout_ms", 3000).put("tty", false).put("env", JSONObject())
    @Test fun binderEvidenceWinsOverPackageVisibilityAndPermissionIsSeparate() {
        assertEquals("not_installed", ShizukuStatePolicy.state(false, false, false, false, false, false))
        assertEquals("permission_required", ShizukuStatePolicy.state(false, true, false, true, false, false))
        assertEquals("version_unsupported", ShizukuStatePolicy.state(true, true, true, false, false, false))
        assertEquals("verification_required", ShizukuStatePolicy.state(true, true, true, true, true, false))
        assertEquals("verified", ShizukuStatePolicy.state(true, true, true, true, true, true))
    }
    @Test fun supersededBindingCallbacksNeverWin() {
        val guard = BindingGeneration()
        val old = guard.advance(); assertTrue(guard.accepts(old))
        val fresh = guard.advance(); assertFalse(guard.accepts(old)); assertTrue(guard.accepts(fresh))
        guard.advance(); assertFalse(guard.accepts(fresh))
    }
    @Test fun explicitWorkdirBackendAndIntegerBoundsAreEnforced() {
        assertEquals("id", ShellProtocol.spec(spec()).command)
        for (cwd in listOf("relative", "/root/../sdcard", "/a//b", "/a/", "/a/./b")) {
            assertThrows(IllegalArgumentException::class.java) { ShellProtocol.spec(spec().put("workdir", cwd)) }
        }
        assertThrows(IllegalArgumentException::class.java) { ShellProtocol.spec(spec().put("backend", "termux_host")) }
        assertThrows(IllegalArgumentException::class.java) { ShellProtocol.spec(spec().put("timeout_ms", "3")) }
        assertThrows(IllegalArgumentException::class.java) { ShellProtocol.spec(spec().put("timeout_ms", 0)) }
    }
    @Test fun fingerprintsIgnoreEnvironmentKeyOrderButNotValues() {
        val first = ShellProtocol.spec(spec().put("env", JSONObject().put("A", "1").put("B", "2")))
        val other = ShellProtocol.spec(spec().put("env", JSONObject().put("B", "2").put("A", "1")))
        assertEquals(first.fingerprint(), other.fingerprint())
        assertNotEquals(first.fingerprint(), ShellProtocol.spec(spec().put("command", "pwd")).fingerprint())
    }
    @Test fun malformedInputCannotConsumeAnAcknowledgement() {
        assertThrows(IllegalArgumentException::class.java) { ShellProtocol.inputs(JSONObject("""{"input":[{"sequence":1,"data":"%%%"}]}""")) }
        assertThrows(IllegalArgumentException::class.java) { ShellProtocol.inputs(JSONObject("""{"input":[{"sequence":0,"data":"YQ=="}]}""")) }
        assertThrows(IllegalArgumentException::class.java) { ShellProtocol.identity("../shell") }
    }
}
