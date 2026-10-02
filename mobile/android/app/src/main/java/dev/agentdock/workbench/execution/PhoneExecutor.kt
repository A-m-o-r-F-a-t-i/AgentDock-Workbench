package dev.agentdock.workbench.execution

import android.content.Context
import android.content.Intent
import android.Manifest
import android.content.pm.PackageManager
import android.os.Build
import androidx.core.app.NotificationManagerCompat
import androidx.core.content.ContextCompat
import dev.agentdock.workbench.data.CoreClient
import dev.agentdock.workbench.data.CoreEndpoint
import dev.agentdock.workbench.data.CoreRequestException
import dev.agentdock.workbench.data.CredentialStore
import dev.agentdock.workbench.data.EndpointPolicy
import dev.agentdock.workbench.termux.LocalCorePairingManager
import kotlinx.coroutines.async
import kotlinx.coroutines.awaitAll
import kotlinx.coroutines.coroutineScope
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.NonCancellable
import kotlinx.coroutines.currentCoroutineContext
import kotlinx.coroutines.delay
import kotlinx.coroutines.ensureActive
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.isActive
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock
import kotlinx.coroutines.withContext
import org.json.JSONArray
import org.json.JSONObject
import java.net.URI
import java.util.UUID

object PhoneExecutorPolicy {
    const val PREFIX = "/internal/runtime/android-executor/"
    fun localOrigin(text: String): URI = EndpointPolicy.resolve(text, false).also {
        require(LocalCorePairingManager.loopbackOrigin(it)) { "本机执行器只绑定本机 Core" }
    }
    fun validInstruction(value: JSONObject): Boolean {
        val id = value.optString("operation_id")
        val action = value.optString("action")
        return Regex("session-[a-f0-9]{24}").matches(id) && value.optLong("revision") > 0 &&
            action in setOf("start", "observe") && value.optString("backend") in setOf("termux_host", "android_shizuku") &&
            value.toString().toByteArray(Charsets.UTF_8).size <= 96 * 1024
    }
}

data class PhoneExecutorState(
    val enabled: Boolean = false,
    val connected: Boolean = false,
    val origin: String = "",
    val message: String = "本机执行器未启用"
)

class PhoneExecutor(private val context: Context, private val credentials: CredentialStore) {
    val termux = TermuxHostBackend(context)
    private val backends = linkedMapOf<String, AndroidExecutionBackend>(termux.name to termux)
    private val preferences = context.getSharedPreferences("phone_executor", Context.MODE_PRIVATE)
    private val control = Mutex()
    private val pump = Mutex()
    private val stateValue = MutableStateFlow(PhoneExecutorState())
    val state = stateValue.asStateFlow()
    @Volatile private var stopRequested = false

    fun addBackend(backend: AndroidExecutionBackend) { check(backends.putIfAbsent(backend.name, backend) == null) }
    fun backend(name: String): AndroidExecutionBackend = checkNotNull(backends[name])
    fun backendEnabled(name: String): Boolean = preferences.getBoolean("backend_$name", name == "termux_host")
    fun setBackendEnabled(name: String, enabled: Boolean) {
        require(name in backends)
        preferences.edit().putBoolean("backend_$name", enabled).apply()
    }
    fun capabilities(): JSONObject = JSONObject().also { result ->
        backends.forEach { (name, backend) ->
            result.put(name, if (backendEnabled(name) && !stopRequested) backend.capability() else JSONObject().put("ready", false).put("state", "disabled"))
        }
    }

    suspend fun verify(name: String): JSONObject = withContext(Dispatchers.IO) { backend(name).verify("connection-probe") }

    suspend fun start(originText: String) = control.withLock {
        check(!pump.isLocked) { "原执行通道尚未停止，请先核对原会话" }
        require(NotificationManagerCompat.from(context).areNotificationsEnabled() &&
            (Build.VERSION.SDK_INT < 33 || ContextCompat.checkSelfPermission(context, Manifest.permission.POST_NOTIFICATIONS) == PackageManager.PERMISSION_GRANTED)) {
            "本机执行器需要可见通知，请先允许通知"
        }
        val origin = PhoneExecutorPolicy.localOrigin(originText)
        val bearer = withContext(Dispatchers.IO) { credentials.getCore(origin) }
        require(bearer.isNotBlank()) { "请先配对本机 Core；不会借用远程节点凭据" }
        val worker = preferences.getString("worker_id", null) ?: UUID.randomUUID().toString().replace("-", "").also {
            preferences.edit().putString("worker_id", it).commit()
        }
        val prior = readLease()
        require(prior == null || prior.optString("origin") == origin.toString()) { "执行器已绑定另一个本机 Origin，请先停止原连接" }
        stopRequested = false
        val registrationCapabilities = JSONObject().also { value ->
            backends.keys.forEach { value.put(it, JSONObject().put("ready", false).put("state", "connecting")) }
        }
        val request = JSONObject().put("protocol", 1).put("worker_id", worker).put("backends", registrationCapabilities)
        if (prior != null) request.put("resume_token", prior.getString("lease_token"))
        val registration = CoreClient(CoreEndpoint(origin, bearer)).post(PhoneExecutorPolicy.PREFIX + "register", request)
        require(registration.optInt("protocol") == 1 && registration.optString("lease_token").length == 64) { "执行器注册协议无效" }
        registration.put("origin", origin.toString())
        withContext(Dispatchers.IO) { credentials.put("android_executor", registration.toString()) }
        preferences.edit().putBoolean("enabled", true).commit()
        try {
            withContext(Dispatchers.Main) { ContextCompat.startForegroundService(context, Intent(context, PhoneExecutorService::class.java)) }
            stateValue.value = PhoneExecutorState(true, false, origin.toString(), "本机执行器正在连接")
        } catch (error: Exception) {
            preferences.edit().putBoolean("enabled", false).commit()
            withContext(NonCancellable) { runCatching { leaseClient(registration).post(PhoneExecutorPolicy.PREFIX + "disconnect") } }
            throw error
        }
    }

    fun stop() {
        stopRequested = true
        preferences.edit().putBoolean("enabled", false).apply()
        stateValue.value = stateValue.value.copy(enabled = false, message = "正在停止接收新命令并核对在途取消")
    }

    private fun readLease(): JSONObject? = credentials.get("android_executor").takeIf { it.isNotBlank() }?.let { runCatching { JSONObject(it) }.getOrNull() }
    private fun leaseClient(lease: JSONObject) = CoreClient(CoreEndpoint(PhoneExecutorPolicy.localOrigin(lease.getString("origin")), lease.getString("lease_token")))

    suspend fun run() = pump.withLock {
        val lease = withContext(Dispatchers.IO) { readLease() } ?: return@withLock
        val node = lease.getString("node_id")
        val origin = lease.getString("origin")
        val client = leaseClient(lease)
        var events = JSONArray()
        var failures = 0
        var stoppingSince = 0L
        var verifiedSince = 0L
        try {
        while (currentCoroutineContext().isActive) {
            if (!preferences.getBoolean("enabled", false)) stopRequested = true
            if (stopRequested && stoppingSince == 0L) stoppingSince = android.os.SystemClock.elapsedRealtime()
            try {
                val now = android.os.SystemClock.elapsedRealtime()
                if (!stopRequested && now - verifiedSince > 30_000) {
                    for ((name, handler) in backends) if (backendEnabled(name)) {
                        try { handler.verify(node) } catch (error: CancellationException) { throw error } catch (_: Exception) { /* Capability reports its current failed state. */ }
                    }
                    verifiedSince = now
                }
                val response = client.post(PhoneExecutorPolicy.PREFIX + "exchange", JSONObject().put("protocol", 1).put("events", events).put("backends", capabilities()))
                require(response.optInt("protocol") == 1 && response.getString("node_id") == node) { "本机 Core 身份已变化，原命令不会重放" }
                events = JSONArray()
                failures = 0
                val commands = response.getJSONArray("commands")
                require(commands.length() <= 4) { "执行批次超过限制" }
                val returned = coroutineScope { (0 until commands.length()).map { index -> async {
                    val command = commands.getJSONObject(index)
                    require(PhoneExecutorPolicy.validInstruction(command)) { "执行指令协议无效" }
                    val event = JSONObject().put("operation_id", command.getString("operation_id")).put("revision", command.getLong("revision"))
                        .put("stdout_offset", command.getLong("stdout_offset")).put("stderr_offset", command.getLong("stderr_offset"))
                    if (stopRequested || !backendEnabled(command.getString("backend"))) command.put("cancel", true)
                    try {
                        val handler = backend(command.getString("backend"))
                        val result = if (command.getString("action") == "start" &&
                            (stopRequested || !backendEnabled(handler.name) || !handler.capability().optBoolean("ready"))) {
                            JSONObject().put("state", "not_started").put("exit_code", -1).put("input_applied", 0)
                        } else handler.handle(node, command)
                        for (key in listOf("state", "exit_code", "stdout", "stderr", "input_applied", "output_limited")) if (result.has(key)) event.put(key, result.get(key))
                        if (!event.has("input_applied")) event.put("input_applied", 0)
                    } catch (error: CancellationException) { throw error
                    } catch (_: Exception) {
                        // Losing the control callback does not establish whether start/write
                        // happened. The next Core instruction only observes the same ID.
                        event.put("state", "unknown").put("input_applied", command.optLong("input_applied", 0))
                    }
                    event
                } }.awaitAll() }
                returned.forEach { events.put(it) }
                stateValue.value = PhoneExecutorState(!stopRequested, true, origin,
                    if (stopRequested) "正在核对原会话取消" else "已连接本机 Core；本批观察 ${commands.length()} 个会话")
                if (stopRequested && commands.length() == 0) break
            } catch (error: CancellationException) { throw error
            } catch (error: Exception) {
                currentCoroutineContext().ensureActive()
                failures++
                stateValue.value = PhoneExecutorState(!stopRequested, false, origin,
                    if (error is CoreRequestException && error.statusCode == 401) "本机执行租约失效，请重新配对；原命令不会重放" else "本机执行通道中断，保留原操作等待核对")
                if (error is CoreRequestException && error.statusCode in 400..499 && error.statusCode != 429) break
            }
            if (stoppingSince > 0 && android.os.SystemClock.elapsedRealtime() - stoppingSince > 15_000) break
            delay(if (failures == 0) (if (events.length() == 0) 2_000 else 300) else (500L * (1L shl failures.coerceAtMost(4))).coerceAtMost(8_000))
        }
        } finally {
            withContext(NonCancellable + Dispatchers.IO) { runCatching { client.post(PhoneExecutorPolicy.PREFIX + "disconnect") } }
            preferences.edit().putBoolean("enabled", false).commit()
            stateValue.value = stateValue.value.copy(enabled = false, connected = false, message = "执行通道已停止；未确认的在途结果仍需核对原会话")
        }
    }
}
