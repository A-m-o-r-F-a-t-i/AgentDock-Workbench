package dev.agentdock.workbench.shizuku

import android.content.ComponentName
import android.content.Context
import android.content.ServiceConnection
import android.content.pm.PackageManager
import android.os.IBinder
import android.os.SystemClock
import dev.agentdock.workbench.BuildConfig
import dev.agentdock.workbench.execution.AndroidExecutionBackend
import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.withContext
import kotlinx.coroutines.withTimeoutOrNull
import org.json.JSONObject
import rikka.shizuku.Shizuku
import java.io.IOException
import java.util.concurrent.ArrayBlockingQueue
import java.util.concurrent.ThreadPoolExecutor
import java.util.concurrent.TimeUnit

internal class BindingGeneration {
    private var generation = 0L
    @Synchronized fun advance(): Long = ++generation
    @Synchronized fun accepts(value: Long): Boolean = generation == value
}

internal object ShizukuStatePolicy {
    fun state(installed: Boolean, binder: Boolean, granted: Boolean, supported: Boolean, bound: Boolean, verified: Boolean): String = when {
        !binder -> if (installed) "not_running" else "not_installed"
        !granted -> "permission_required"
        !supported -> "version_unsupported"
        !bound -> "binding_required"
        !verified -> "verification_required"
        else -> "verified"
    }
}

data class ShizukuViewState(val state: String = "not_running", val uid: Int? = null, val serverVersion: Int? = null)

/** Android authorization and binding are independent of the Termux channel. */
class ShizukuBackend(private val context: Context) : AndroidExecutionBackend {
    override val name = "android_shizuku"
    private val lock = Any()
    private val generation = BindingGeneration()
    private val stateValue = MutableStateFlow(ShizukuViewState())
    val state = stateValue.asStateFlow()
    private var service: IPhoneShell? = null
    private var attempt: Attempt? = null
    @Volatile private var verifiedAt = 0L
    @Volatile private var actualUid: Int? = null
    // Binder transactions cannot be interrupted reliably. Bound the worker and
    // queue population instead of creating an unbounded thread on each timeout.
    private val calls = ThreadPoolExecutor(2, 2, 30, TimeUnit.SECONDS, ArrayBlockingQueue<Runnable>(8),
        { work -> Thread(work, "agentdock-shizuku-control").apply { isDaemon = true } }, ThreadPoolExecutor.AbortPolicy())
    private val args = Shizuku.UserServiceArgs(ComponentName(context.packageName, PhoneShellService::class.java.name))
        .tag("agentdock-phone-shell-v1").version(BuildConfig.VERSION_CODE).daemon(true)
        .processNameSuffix("phone_shell").debuggable(BuildConfig.DEBUG)
    private val received = Shizuku.OnBinderReceivedListener { invalidate("verification_required"); refresh() }
    private val dead = Shizuku.OnBinderDeadListener { invalidate("not_running"); refresh() }
    private val permission = Shizuku.OnRequestPermissionResultListener { code, _ ->
        if (code == REQUEST_PERMISSION) { invalidate("verification_required"); refresh() }
    }

    init {
        Shizuku.addBinderReceivedListenerSticky(received)
        Shizuku.addBinderDeadListener(dead)
        Shizuku.addRequestPermissionResultListener(permission)
    }

    private fun invalidate(reason: String) {
        val old = synchronized(lock) {
            generation.advance(); service = null; verifiedAt = 0; actualUid = null
            attempt.also { attempt = null }
        }
        old?.waiter?.completeExceptionally(IOException(reason))
        stateValue.value = ShizukuViewState(reason)
    }

    fun refresh(): ShizukuViewState {
        val binder = runCatching { Shizuku.pingBinder() }.getOrDefault(false)
        val installed = binder || runCatching { context.packageManager.getPackageInfo(PACKAGE, 0) }.isSuccess
        val granted = binder && runCatching { Shizuku.checkSelfPermission() == PackageManager.PERMISSION_GRANTED }.getOrDefault(false)
        val version = if (binder) runCatching { Shizuku.getVersion() }.getOrNull() else null
        val bound = synchronized(lock) { service?.asBinder()?.isBinderAlive == true }
        val verified = verifiedAt > 0 && SystemClock.elapsedRealtime() - verifiedAt < 60_000
        return ShizukuViewState(ShizukuStatePolicy.state(installed, binder, granted, (version ?: 0) >= 13, bound, verified),
            if (granted && bound) actualUid else null, version).also { stateValue.value = it }
    }
    override fun capability(): JSONObject {
        val value = refresh()
        return JSONObject().put("ready", value.state == "verified").put("state", value.state).also { result -> value.uid?.let { result.put("uid", it) } }
    }
    fun requestPermission() {
        check(runCatching { Shizuku.pingBinder() }.getOrDefault(false)) { "请先在 Shizuku 中启动服务" }
        Shizuku.requestPermission(REQUEST_PERMISSION)
    }

    override suspend fun verify(nodeId: String): JSONObject {
        verifiedAt = 0
        val response = invoke(nodeId, JSONObject().put("action", "probe"))
        val uid = response.getInt("uid")
        require(response.optInt("protocol") == 1 && response.optBoolean("ready") && uid in setOf(0, 2000)) { "unexpected_shizuku_identity" }
        require(uid == Shizuku.getUid()) { "shizuku_identity_changed" }
        actualUid = uid; verifiedAt = SystemClock.elapsedRealtime()
        return capability()
    }
    override suspend fun handle(nodeId: String, instruction: JSONObject): JSONObject {
        val result = invoke(nodeId, instruction)
        require(result.optString("state") in ShellProtocol.terminal + setOf("queued", "running", "cancel_requested", "unknown")) { "shizuku_result_unconfirmed" }
        return result
    }

    private suspend fun invoke(nodeId: String, request: JSONObject): JSONObject {
        ShellProtocol.node(nodeId)
        val text = request.toString()
        require(text.toByteArray(Charsets.UTF_8).size <= ShellProtocol.MAX_REQUEST)
        val api = ensureBound()
        val returned = CompletableDeferred<String>()
        calls.execute {
            try { returned.complete(api.control(nodeId, text)) }
            catch (_: Exception) { returned.completeExceptionally(IOException("shizuku_control_disconnected")) }
        }
        val raw = withTimeoutOrNull(4_000) { returned.await() } ?: throw IOException("shizuku_control_timeout")
        require(raw.toByteArray(Charsets.UTF_8).size <= ShellProtocol.MAX_REQUEST) { "shizuku_response_too_large" }
        return JSONObject(raw).also { require(!it.has("error")) { "shizuku_result_unconfirmed" } }
    }

    private suspend fun ensureBound(): IPhoneShell {
        val status = refresh().state
        check(status in setOf("binding_required", "verification_required", "verified")) { status }
        val selected: Attempt
        var new = false
        synchronized(lock) {
            service?.takeIf { it.asBinder().isBinderAlive }?.let { return it }
            selected = attempt ?: Attempt(generation.advance()).also { attempt = it; new = true }
        }
        if (new) bind(selected)
        val value = withTimeoutOrNull(8_000) { selected.waiter.await() }
        if (value != null) return value
        val expired = synchronized(lock) {
            if (attempt === selected) { generation.advance(); attempt = null; true } else false
        }
        if (expired) {
            selected.waiter.completeExceptionally(IOException("shizuku_bind_timeout"))
            selected.connection?.let { conn -> withContext(Dispatchers.IO) { runCatching { Shizuku.unbindUserService(args, conn, false) } } }
        }
        throw IOException("shizuku_bind_timeout")
    }
    private fun bind(selected: Attempt) {
        val connection = object : ServiceConnection {
            override fun onServiceConnected(name: ComponentName?, binder: IBinder?) {
                val api = binder?.takeIf { it.isBinderAlive }?.let { IPhoneShell.Stub.asInterface(it) }
                val accepted = synchronized(lock) {
                    if (generation.accepts(selected.generation) && attempt === selected && api != null) {
                        service = api; attempt = null; true
                    } else false
                }
                if (accepted) selected.waiter.complete(checkNotNull(api))
                else {
                    selected.waiter.completeExceptionally(IOException("shizuku_binding_expired"))
                    runCatching { Shizuku.unbindUserService(args, this, false) }
                }
                refresh()
            }
            override fun onServiceDisconnected(name: ComponentName?) { if (generation.accepts(selected.generation)) invalidate("binding_disconnected") }
            override fun onBindingDied(name: ComponentName?) { if (generation.accepts(selected.generation)) invalidate("binding_died") }
            override fun onNullBinding(name: ComponentName?) { if (generation.accepts(selected.generation)) invalidate("binding_null") }
        }
        selected.connection = connection
        if (!generation.accepts(selected.generation)) return
        try { Shizuku.bindUserService(args, connection) }
        catch (_: Exception) {
            if (generation.accepts(selected.generation)) invalidate("bind_rejected")
            selected.waiter.completeExceptionally(IOException("shizuku_bind_rejected"))
        }
    }
    private class Attempt(val generation: Long) {
        val waiter = CompletableDeferred<IPhoneShell>()
        var connection: ServiceConnection? = null
    }
    companion object { const val PACKAGE = "moe.shizuku.privileged.api"; private const val REQUEST_PERMISSION = 17833 }
}
