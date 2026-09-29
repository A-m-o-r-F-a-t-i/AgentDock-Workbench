package dev.agentdock.workbench.execution

import android.app.PendingIntent
import android.app.Service
import android.content.Context
import android.content.Intent
import android.content.pm.PackageManager
import android.os.Build
import android.os.IBinder
import androidx.core.content.ContextCompat
import dev.agentdock.workbench.WorkbenchApplication
import dev.agentdock.workbench.termux.TermuxCallbackDecision
import dev.agentdock.workbench.termux.TermuxCallbackProtocol
import dev.agentdock.workbench.termux.TermuxContract
import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext
import kotlinx.coroutines.withTimeoutOrNull
import org.json.JSONObject
import java.io.IOException
import java.util.UUID
import java.util.concurrent.ConcurrentHashMap

interface AndroidExecutionBackend {
    val name: String
    fun capability(): JSONObject
    suspend fun verify(nodeId: String): JSONObject
    suspend fun handle(nodeId: String, instruction: JSONObject): JSONObject
}

/** Only the fixed host-control module is launched through RUN_COMMAND. */
class TermuxHostBackend(private val context: Context) : AndroidExecutionBackend {
    override val name = "termux_host"
    private val pending = ConcurrentHashMap<String, CompletableDeferred<JSONObject>>()
    @Volatile private var verifiedAt = 0L
    @Volatile private var uid: Int? = null

    override fun capability(): JSONObject {
        val installed = runCatching { context.packageManager.getPackageInfo(TermuxContract.PACKAGE, 0) }.isSuccess
        val permission = ContextCompat.checkSelfPermission(context, TermuxContract.PERMISSION_RUN_COMMAND) == PackageManager.PERMISSION_GRANTED
        val verified = installed && permission && android.os.SystemClock.elapsedRealtime() - verifiedAt < 60_000 && verifiedAt > 0
        return JSONObject().put("ready", verified).put("state", when {
            !installed -> "not_installed"
            !permission -> "permission_required"
            !verified -> "verification_required"
            else -> "verified"
        }).also { value -> uid?.let { value.put("uid", it) } }
    }

    override suspend fun verify(nodeId: String): JSONObject {
        val result = invoke(nodeId, JSONObject().put("action", "probe"))
        if (result.optInt("protocol") != 1 || !result.optBoolean("ready")) throw IOException("TERMUX_HOST_PROBE_FAILED")
        uid = result.getInt("uid")
        verifiedAt = android.os.SystemClock.elapsedRealtime()
        return capability()
    }

    override suspend fun handle(nodeId: String, instruction: JSONObject): JSONObject {
        val request = JSONObject()
        for (key in listOf("action", "operation_id", "spec", "stdout_offset", "stderr_offset", "cancel", "eof", "input")) {
            if (instruction.has(key)) request.put(key, instruction.get(key))
        }
        val result = invoke(nodeId, request)
        verifiedAt = android.os.SystemClock.elapsedRealtime()
        return result
    }

    private suspend fun invoke(nodeId: String, body: JSONObject): JSONObject = withContext(Dispatchers.IO) {
        if (ContextCompat.checkSelfPermission(context, TermuxContract.PERMISSION_RUN_COMMAND) != PackageManager.PERMISSION_GRANTED) throw IOException("TERMUX_PERMISSION_REQUIRED")
        require(pending.size < 16) { "TERMUX_CALLBACK_CAPACITY" }
        val nonce = UUID.randomUUID().toString().replace("-", "")
        val wait = CompletableDeferred<JSONObject>()
        val callbackIntent = Intent(context, HostResultService::class.java).setAction("${context.packageName}.HOST_RESULT.$nonce").putExtra("nonce", nonce)
        val callback = PendingIntent.getService(context, 0, callbackIntent, PendingIntent.FLAG_UPDATE_CURRENT or
            if (Build.VERSION.SDK_INT >= 31) PendingIntent.FLAG_MUTABLE else 0)
        pending[nonce] = wait
        try {
            val request = JSONObject(body.toString()).put("protocol", 1).put("nonce", nonce).put("node_id", nodeId).toString()
            require(request.toByteArray(Charsets.UTF_8).size <= 96 * 1024) { "HOST_REQUEST_TOO_LARGE" }
            val intent = Intent(TermuxContract.ACTION_RUN_COMMAND).apply {
                component = TermuxContract.RUN_COMMAND_COMPONENT
                setPackage(TermuxContract.PACKAGE)
                putExtra(TermuxContract.EXTRA_COMMAND_PATH, "/data/data/com.termux/files/usr/bin/python")
                putExtra(TermuxContract.EXTRA_ARGUMENTS, arrayOf("/data/data/com.termux/files/home/.termux/tasker/agentdock_host_executor.py"))
                putExtra(TermuxContract.EXTRA_STDIN, request)
                putExtra(TermuxContract.EXTRA_WORKDIR, TermuxContract.WORKDIR)
                putExtra(TermuxContract.EXTRA_BACKGROUND, true)
                putExtra(TermuxContract.EXTRA_PENDING_INTENT, callback)
            }
            context.startService(intent)
            withTimeoutOrNull(10_000) { wait.await() } ?: throw IOException("TERMUX_HOST_CALLBACK_TIMEOUT")
        } finally {
            pending.remove(nonce)
            callback.cancel()
        }
    }

    fun receive(intent: Intent) {
        val nonce = intent.getStringExtra("nonce") ?: return
        val wait = pending[nonce] ?: return
        if (intent.action != "${context.packageName}.HOST_RESULT.$nonce") return
        val bundle = intent.getBundleExtra(TermuxContract.EXTRA_RESULT_BUNDLE) ?: intent.extras
        @Suppress("DEPRECATION")
        val fields = bundle?.let { values -> TermuxCallbackProtocol.resultKeys.filter { values.containsKey(it) }.associateWith { values.get(it) } }
        when (val result = TermuxCallbackProtocol.decode(fields)) {
            TermuxCallbackDecision.AwaitCompletion -> return
            is TermuxCallbackDecision.Result -> {
                val value = runCatching { JSONObject(result.stdout) }.getOrNull()
                if (result.exitCode != 0 || value == null || value.optString("nonce") != nonce || value.optInt("protocol") != 1 || !value.optBoolean("ok")) {
                    wait.completeExceptionally(IOException("TERMUX_HOST_RESULT_INVALID"))
                } else wait.complete(value.getJSONObject("result"))
            }
            else -> wait.completeExceptionally(IOException("TERMUX_HOST_RESULT_UNCONFIRMED"))
        }
    }
}

/** Bounded parsing only; neither arbitrary I/O nor command execution runs here. */
class HostResultService : Service() {
    override fun onBind(intent: Intent?): IBinder? = null
    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        try { if (intent != null) (application as WorkbenchApplication).graph.phoneExecutor.termux.receive(intent) }
        finally { stopSelf(startId) }
        return START_NOT_STICKY
    }
}
