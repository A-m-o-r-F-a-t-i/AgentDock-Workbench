package dev.agentdock.workbench.lifecycle

import android.content.BroadcastReceiver
import android.content.Context
import android.content.Intent
import android.util.Log
import kotlinx.coroutines.CancellationException
import dev.agentdock.workbench.WorkbenchApplication
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import org.json.JSONObject

class GuardianActionReceiver : BroadcastReceiver() {
    override fun onReceive(context: Context, intent: Intent) {
        val result = goAsync()
        CoroutineScope(Dispatchers.IO).launch {
            try {
                val graph = (context.applicationContext as WorkbenchApplication).graph
                when (intent.action) {
                    ACTION_PAUSE -> graph.guardian.setPaused(true)
                    ACTION_RESUME -> graph.guardian.setPaused(false)
                    ACTION_STOP_CORE -> {
                        graph.termux.dispatch("stop", JSONObject().put("source", "notification"))
                    }
                }
            } catch (error: CancellationException) { throw error
            } catch (_: Exception) {
                Log.w("AgentDockGuardian", "Guardian action failed; retained operation state requires review")
            } finally {
                result.finish()
            }
        }
    }

    companion object {
        const val ACTION_PAUSE = "dev.agentdock.workbench.guardian.PAUSE"
        const val ACTION_RESUME = "dev.agentdock.workbench.guardian.RESUME"
        const val ACTION_STOP_CORE = "dev.agentdock.workbench.guardian.STOP_CORE"
    }
}
