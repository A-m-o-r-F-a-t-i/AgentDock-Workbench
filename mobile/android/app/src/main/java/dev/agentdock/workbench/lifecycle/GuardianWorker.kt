package dev.agentdock.workbench.lifecycle

import android.content.Context
import androidx.work.CoroutineWorker
import androidx.work.WorkerParameters
import dev.agentdock.workbench.WorkbenchApplication
import kotlinx.coroutines.CancellationException
import org.json.JSONObject

class GuardianWorker(context: Context, parameters: WorkerParameters) : CoroutineWorker(context, parameters) {
    override suspend fun doWork(): Result {
        val graph = (applicationContext as WorkbenchApplication).graph
        val settings = graph.settings.current()
        val operation = GuardianPolicy.operation(settings, inputData.getString(CHECK_KIND) == BOOT_CHECK) ?: return Result.success()
        if (GuardianConditions.waitReason(applicationContext, settings) != null) return Result.success()
        return try {
            val pending = graph.termux.dispatch(operation, JSONObject().put("source", "work_manager"))
            val receipt = graph.termux.awaitCompletion(pending.operationId)
            when (GuardianWorkResultPolicy.classify(receipt)) {
                GuardianWorkOutcome.Success -> Result.success()
                GuardianWorkOutcome.Retry -> Result.retry()
                GuardianWorkOutcome.Failure -> Result.failure()
            }
        } catch (error: CancellationException) { throw error
        } catch (_: Exception) { Result.retry() }
    }

    companion object {
        const val CHECK_KIND = "check_kind"
        const val BOOT_CHECK = "boot_read_only"
    }
}
