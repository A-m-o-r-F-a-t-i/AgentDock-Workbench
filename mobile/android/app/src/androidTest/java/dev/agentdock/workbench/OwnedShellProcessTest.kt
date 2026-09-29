package dev.agentdock.workbench

import android.content.Context
import android.os.Process
import androidx.test.core.app.ApplicationProvider
import dev.agentdock.workbench.shizuku.NativeCommand
import dev.agentdock.workbench.shizuku.OwnedShellRegistry
import org.json.JSONArray
import org.json.JSONObject
import org.junit.After
import org.junit.Assert.*
import org.junit.Before
import org.junit.Test
import java.io.ByteArrayOutputStream
import java.io.File
import java.util.Base64
import java.util.UUID

/** Real native processes in the emulator's app sandbox; not real Shizuku authorization. */
class OwnedShellProcessTest {
    private lateinit var registry: OwnedShellRegistry
    private lateinit var root: File
    private val node = "emulator-native-contract"
    private val id = "session-" + "a".repeat(24)
    @Before fun setup() {
        val context = ApplicationProvider.getApplicationContext<Context>()
        root = File(context.cacheDir, "shell-${UUID.randomUUID()}").apply { mkdirs() }
        NativeCommand.initialize(context.applicationInfo.nativeLibraryDir)
        registry = OwnedShellRegistry(Process.myUid())
    }
    @After fun cleanup() { registry.shutdown(); root.deleteRecursively() }
    private fun request(action: String = "observe", identity: String = id) = JSONObject().put("action", action).put("operation_id", identity)
        .put("stdout_offset", 0).put("stderr_offset", 0).put("cancel", false).put("eof", false)
    private fun start(command: String, timeout: Int = 4000, tty: Boolean = false, identity: String = id): JSONObject {
        val req = request("start", identity).put("spec", JSONObject().put("backend", "android_shizuku").put("command", command)
            .put("workdir", root.absolutePath).put("env", JSONObject()).put("timeout_ms", timeout).put("tty", tty))
        registry.control(node, req)
        return req
    }
    private fun terminal(identity: String = id, limitMs: Long = 10000): JSONObject {
        val out = ByteArrayOutputStream(); val err = ByteArrayOutputStream()
        val deadline = System.nanoTime() + limitMs * 1_000_000L
        while (System.nanoTime() < deadline) {
            val response = registry.control(node, request(identity = identity).put("stdout_offset", out.size()).put("stderr_offset", err.size()))
            val stdout = Base64.getDecoder().decode(response.optString("stdout"))
            val stderr = Base64.getDecoder().decode(response.optString("stderr"))
            out.write(stdout); err.write(stderr)
            if (response.getString("state") in setOf("exited", "cancelled", "timeout", "not_started")) {
                return response.put("all_stdout", out.toString("UTF-8")).put("all_stderr", err.toString("UTF-8")).put("output_bytes", out.size() + err.size())
            }
            if (stdout.isEmpty() && stderr.isEmpty()) Thread.sleep(25)
        }
        throw AssertionError("Original native process did not settle: $identity")
    }
    @Test fun capturesExitAndIndependentStreams() {
        start("printf output; printf error >&2; exit 7")
        val result = terminal()
        assertEquals(7, result.getInt("exit_code")); assertEquals("output", result.getString("all_stdout")); assertEquals("error", result.getString("all_stderr"))
    }
    @Test fun duplicateStartAndCursorDoNotRepeatEffects() {
        val started = start("printf x >> count; printf '中文😀'")
        terminal(); registry.control(node, started)
        assertEquals("x", File(root, "count").readText())
        assertEquals("中文😀", String(Base64.getDecoder().decode(registry.control(node, request()).getString("stdout")), Charsets.UTF_8))
        val first = registry.control(node, request()).getString("stdout")
        assertEquals(first, registry.control(node, request()).getString("stdout"))
    }
    @Test fun stdinIsAcknowledgedExactlyOnce() {
        start("cat")
        val input = JSONArray().put(JSONObject().put("sequence", 1).put("data", Base64.getEncoder().encodeToString("hello\n".toByteArray())))
        val control = request().put("input", input).put("eof", true)
        registry.control(node, control); registry.control(node, control)
        val result = terminal()
        assertEquals(1, result.getInt("input_applied")); assertEquals("hello\n", result.getString("all_stdout"))
    }
    @Test fun nativeDeadlineDoesNotNeedAnotherControlCall() {
        start("sleep 30", timeout = 250)
        Thread.sleep(700)
        assertEquals("timeout", terminal().getString("state"))
    }
    @Test fun fullStdinCannotBlockCancellationOrDeadline() {
        start("sleep 30", timeout = 350)
        registry.control(node, request().put("input", JSONArray().put(JSONObject().put("sequence", 1).put("data", Base64.getEncoder().encodeToString(ByteArray(16384) { 120 })))))
        assertEquals("timeout", terminal().getString("state"))
    }
    @Test fun cancellingOneGroupLeavesTheOtherJobIntact() {
        val sibling = "session-" + "b".repeat(24)
        start("sleep 30", timeout = 10000)
        start("sleep 1; printf sibling", identity = sibling)
        registry.control(node, request().put("cancel", true))
        assertEquals("cancelled", terminal().getString("state"))
        assertEquals("sibling", terminal(sibling).getString("all_stdout"))
    }
    @Test fun ptyHasAControllingTerminal() {
        start("test -t 0 && test -t 1 && printf controlling >/dev/tty", tty = true)
        val result = terminal(); assertEquals(0, result.getInt("exit_code")); assertTrue(result.getString("all_stdout").contains("controlling"))
    }
    @Test fun badWorkingDirectoryIsNotStarted() {
        val req = request("start").put("spec", JSONObject().put("backend", "android_shizuku").put("command", "echo unexpected")
            .put("workdir", root.absolutePath + "/missing").put("env", JSONObject()).put("timeout_ms", 1000).put("tty", false))
        registry.control(node, req)
        assertEquals("not_started", terminal().getString("state"))
    }
    @Test fun outputOverflowIsExplicitAndBounded() {
        start("head -c 5000000 /dev/zero", timeout = 5000)
        val result = terminal(limitMs = 15000)
        assertTrue(result.getBoolean("output_limited")); assertTrue(result.getInt("output_bytes") <= 4 * 1024 * 1024)
    }
}
