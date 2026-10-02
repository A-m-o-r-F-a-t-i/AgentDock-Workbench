package dev.agentdock.workbench.execution

import org.json.JSONObject
import org.junit.Assert.*
import org.junit.Test

class PhoneDiagnosticsTest {
    @Test fun reportDropsCredentialsCommandsAndUnknownChannels() {
        val input = JSONObject().put("android_shizuku", JSONObject().put("state", "verified").put("ready", true).put("uid", 2000)
            .put("lease_token", "SHOULD_NOT_APPEAR").put("command", "PRIVATE_COMMAND").put("env", JSONObject().put("API_KEY", "PRIVATE_KEY")))
            .put("unreviewed", JSONObject().put("state", "PRIVATE_CHANNEL"))
        val report = PhoneDiagnostics.snapshot("1.1.8", "a".repeat(40), true, true, input)
        val text = report.toString()
        for (secret in listOf("SHOULD_NOT_APPEAR", "PRIVATE_COMMAND", "PRIVATE_KEY", "PRIVATE_CHANNEL", "lease_token")) assertFalse(text.contains(secret))
        assertEquals(2000, report.getJSONObject("backends").getJSONObject("android_shizuku").getInt("uid"))
        assertEquals(2, report.getJSONObject("backends").length())
    }
    @Test fun rawErrorTextCannotBecomeAConnectionState() {
        val report = PhoneDiagnostics.snapshot("1.1.8", "not a sha", false, false,
            JSONObject().put("termux_host", JSONObject().put("state", "Authorization: Bearer PRIVATE").put("ready", "true")))
        assertFalse(report.toString().contains("PRIVATE"))
        assertEquals("unknown", report.getJSONObject("backends").getJSONObject("termux_host").getString("state"))
        assertFalse(report.getJSONObject("backends").getJSONObject("termux_host").getBoolean("ready"))
    }
}
