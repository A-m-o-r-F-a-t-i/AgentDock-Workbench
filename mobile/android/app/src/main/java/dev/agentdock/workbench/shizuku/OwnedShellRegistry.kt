package dev.agentdock.workbench.shizuku

import org.json.JSONObject
import java.io.ByteArrayOutputStream
import java.util.Base64
import java.util.UUID
import java.util.concurrent.atomic.AtomicInteger

/** Short control calls; each admitted process has its own independent supervisor. */
internal class OwnedShellRegistry(private val uid: Int) {
    private val jobs = linkedMapOf<String, OwnedJob>()
    private val seen = hashSetOf<String>()
    private val budget = AtomicInteger()
    private var closing = false
    val instance: String = UUID.randomUUID().toString()

    @Synchronized fun control(nodeId: String, request: JSONObject): JSONObject {
        ShellProtocol.node(nodeId)
        require(request.toString().toByteArray(Charsets.UTF_8).size <= ShellProtocol.MAX_REQUEST)
        val action = request.getString("action")
        if (action == "probe") return JSONObject().put("protocol", 1).put("uid", uid).put("ready", !closing).put("instance", instance)
        check(!closing) { "service_stopping" }
        val id = ShellProtocol.identity(request.getString("operation_id"))
        val key = "$nodeId:$id"
        require(action in setOf("start", "observe"))
        val inputs = ShellProtocol.inputs(request)
        val outOffset = ShellProtocol.integer(request, "stdout_offset", 0, OUTPUT_LIMIT.toLong()).toInt()
        val errOffset = ShellProtocol.integer(request, "stderr_offset", 0, OUTPUT_LIMIT.toLong()).toInt()
        require(request.opt("cancel") is Boolean && request.opt("eof") is Boolean)
        if (action == "start") {
            val spec = ShellProtocol.spec(request.getJSONObject("spec"))
            val existing = jobs[key]
            if (existing != null) require(existing.fingerprint == spec.fingerprint()) { "operation_conflict" }
            else if (key !in seen) {
                val stale = jobs.filterValues { it.reapable() && it.finishedAt > 0 && System.nanoTime() - it.finishedAt > 3600_000_000_000L }.keys.toList()
                stale.forEach { jobs.remove(it)?.discardOutput() }
                require(jobs.size < 128 && seen.size < 8192 && jobs.values.count { !it.finished } < 4) { "capacity" }
                val job = OwnedJob(spec, budget)
                seen.add(key)
                jobs[key] = job // Reserve identity before any code can run.
                if (request.getBoolean("cancel")) job.cancel()
                job.launch()
            }
        }
        val job = jobs[key] ?: return JSONObject().put("state", "unknown").put("input_applied", request.optLong("input_applied", 0))
        job.control(inputs, request.getBoolean("cancel"), request.getBoolean("eof"))
        return job.snapshot(outOffset, errOffset)
    }

    fun shutdown() {
        val pending = synchronized(this) { closing = true; jobs.values.toList().also { list -> list.forEach { it.cancel() } } }
        val deadline = System.nanoTime() + 3_000_000_000L
        pending.forEach { it.await(((deadline - System.nanoTime()) / 1_000_000L).coerceAtLeast(1)) }
    }

    companion object { const val OUTPUT_LIMIT = 4 * 1024 * 1024; const val TOTAL_OUTPUT_LIMIT = 16 * 1024 * 1024 }
}

private class OwnedJob(private val spec: ShellSpec, private val budget: AtomicInteger) {
    val fingerprint = spec.fingerprint()
    private val out = ByteArrayOutputStream()
    private val err = ByteArrayOutputStream()
    private val inputs = sortedMapOf<Long, ByteArray>()
    private var inputApplied = 0L
    private var inputOffset = 0
    private var eof = false
    private var cancelRequested = false
    private var state = "queued"
    private var code: Int? = null
    private var limited = false
    private val started = System.nanoTime()
    private val deadline = started + spec.timeoutMs * 1_000_000L
    @Volatile var finishedAt = 0L; private set
    @Volatile var finished = false; private set
    private val thread = Thread(::supervise, "agentdock-owned-shell").apply { isDaemon = true }
    @Synchronized fun reapable(): Boolean = finished && state in ShellProtocol.terminal
    fun launch() { thread.start() }
    @Synchronized fun cancel() { cancelRequested = true }
    fun await(milliseconds: Long) { if (thread.isAlive) thread.join(milliseconds) }
    @Synchronized fun discardOutput() { budget.addAndGet(-out.size() - err.size()); out.reset(); err.reset() }

    @Synchronized fun control(values: List<Pair<Long, ByteArray>>, cancel: Boolean, close: Boolean) {
        if (cancel) cancelRequested = true
        if (finished) return
        for ((seq, data) in values) {
            if (seq <= inputApplied) continue
            val existing = inputs[seq]
            if (existing != null) require(existing.contentEquals(data)) { "input_conflict" }
            else { require(inputs.size < 4 && !eof) { "stdin_closed_or_full" }; inputs[seq] = data }
        }
        if (close) eof = true
    }

    @Synchronized fun snapshot(outOffset: Int, errOffset: Int): JSONObject {
        require(outOffset <= out.size() && errOffset <= err.size()) { "output_cursor_ahead" }
        val stdout = out.toByteArray().let { it.copyOfRange(outOffset, minOf(it.size, outOffset + ShellProtocol.CHUNK)) }
        val stderr = err.toByteArray().let { it.copyOfRange(errOffset, minOf(it.size, errOffset + ShellProtocol.CHUNK)) }
        val remaining = outOffset + stdout.size < out.size() || errOffset + stderr.size < err.size()
        val reported = if (finished && remaining && state in ShellProtocol.terminal) "running" else state
        return JSONObject().put("state", reported).put("stdout", Base64.getEncoder().encodeToString(stdout))
            .put("stderr", Base64.getEncoder().encodeToString(stderr)).put("input_applied", inputApplied)
            .put("output_limited", limited).also { if (reported in ShellProtocol.terminal) it.put("exit_code", code ?: -1) }
    }

    @Synchronized private fun append(stream: Int, data: ByteArray) {
        val local = OwnedShellRegistry.OUTPUT_LIMIT - out.size() - err.size()
        var accepted: Int
        while (true) {
            val total = budget.get()
            accepted = minOf(data.size, local, (OwnedShellRegistry.TOTAL_OUTPUT_LIMIT - total).coerceAtLeast(0))
            if (budget.compareAndSet(total, total + accepted)) break
        }
        if (accepted < data.size) limited = true
        (if (stream == 1) out else err).write(data, 0, accepted)
    }
    private fun readAvailable(handle: Long): Boolean {
        var allEOF = true
        for (stream in 1..2) {
            var eofNow = false
            repeat(16) {
                if (!eofNow) {
                    val bytes = NativeCommand.read(handle, stream)
                    if (bytes == null) eofNow = true else if (bytes.isNotEmpty()) append(stream, bytes)
                }
            }
            allEOF = allEOF && eofNow
        }
        return allEOF
    }
    private fun supervise() {
        var handle = 0L
        var reason = "exited"
        try {
            if (synchronized(this) { cancelRequested }) { complete("cancelled", -1); return }
            if (System.nanoTime() >= deadline) { complete("timeout", -1); return }
            handle = NativeCommand.spawn(spec.command.toByteArray(Charsets.UTF_8), spec.workdir.toByteArray(Charsets.UTF_8), spec.environment(), spec.tty)
            synchronized(this) { state = "running" }
            var inputClosed = false
            var observedExit = NativeCommand.RUNNING
            while (observedExit == NativeCommand.RUNNING) {
                readAvailable(handle)
                val terminate = synchronized(this) {
                    when {
                        cancelRequested -> { reason = "cancelled"; true }
                        System.nanoTime() >= deadline -> { reason = "timeout"; true }
                        limited -> true
                        else -> false
                    }
                }
                if (terminate) { NativeCommand.terminate(handle); synchronized(this) { state = "cancel_requested" } }
                if (!terminate && !inputClosed) {
                    val next = synchronized(this) { inputs[inputApplied + 1]?.let { Triple(inputApplied + 1, it, inputOffset) } }
                    if (next != null) {
                        val written = NativeCommand.write(handle, next.second, next.third)
                        synchronized(this) {
                            inputOffset += written
                            if (inputOffset == next.second.size) { inputApplied = next.first; inputs.remove(next.first); inputOffset = 0 }
                        }
                    }
                    if (!spec.tty && synchronized(this) { eof && inputs.isEmpty() }) { NativeCommand.closeInput(handle); inputClosed = true }
                }
                observedExit = NativeCommand.poll(handle)
                if (observedExit == NativeCommand.RUNNING) Thread.sleep(20)
            }
            // The leader remains unreaped while its group is cleaned and drained.
            NativeCommand.terminate(handle)
            val drainDeadline = System.nanoTime() + 1_000_000_000L
            while (!readAvailable(handle) && System.nanoTime() < drainDeadline) Thread.sleep(10)
            NativeCommand.release(handle); handle = 0
            complete(reason, observedExit)
        } catch (_: Exception) {
            if (handle == 0L) complete("not_started", -1)
            else {
                var reaped = false
                runCatching {
                    NativeCommand.terminate(handle)
                    val stop = System.nanoTime() + 1_000_000_000L
                    while (NativeCommand.poll(handle) == NativeCommand.RUNNING && System.nanoTime() < stop) Thread.sleep(20)
                    if (NativeCommand.poll(handle) != NativeCommand.RUNNING) {
                        readAvailable(handle); NativeCommand.release(handle); handle = 0; reaped = true
                    }
                }
                // I/O failure cannot confirm how much input reached the command.
                synchronized(this) { state = "unknown"; finished = reaped; if (reaped) finishedAt = System.nanoTime() }
            }
        }
    }
    @Synchronized private fun complete(value: String, exit: Int) { state = value; code = exit; finished = true; finishedAt = System.nanoTime() }
}
