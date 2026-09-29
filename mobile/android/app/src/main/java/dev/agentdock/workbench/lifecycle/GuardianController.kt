package dev.agentdock.workbench.lifecycle

import dev.agentdock.workbench.model.WorkbenchSettings
import kotlinx.coroutines.NonCancellable
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock
import kotlinx.coroutines.withContext

interface GuardianEffects {
    fun notificationsAllowed(): Boolean
    fun configure(settings: WorkbenchSettings)
    fun start()
    fun stop()
}

/** One serialized admission path shared by settings, quick tile and notification. */
class GuardianController(
    private val read: suspend () -> WorkbenchSettings,
    private val update: suspend ((WorkbenchSettings) -> WorkbenchSettings) -> Unit,
    private val effects: GuardianEffects
) {
    private val mutex = Mutex()

    suspend fun setEnabled(enabled: Boolean) = change {
        it.copy(guardianEnabled = enabled, guardianPaused = if (enabled) false else it.guardianPaused)
    }

    suspend fun setPaused(paused: Boolean) = change { it.copy(guardianPaused = paused) }

    suspend fun setNotifications(enabled: Boolean) = change {
        if (enabled) it.copy(notificationsEnabled = true)
        else it.copy(notificationsEnabled = false, guardianEnabled = false, guardianPaused = false)
    }

    suspend fun toggle() = change {
        if (!it.guardianEnabled) it.copy(guardianEnabled = true, guardianPaused = false)
        else it.copy(guardianPaused = !it.guardianPaused)
    }

    private suspend fun change(transform: (WorkbenchSettings) -> WorkbenchSettings) = mutex.withLock {
        withContext(NonCancellable) {
            val before = read()
            val desired = transform(before)
            if (GuardianPolicy.active(desired)) {
                check(desired.notificationsEnabled && effects.notificationsAllowed()) {
                    "通知未启用或系统未授权，未启动守护；请先在设置中启用通知。"
                }
            }
            update { copyControls(it, desired) }
            try {
                effects.configure(read())
                if (GuardianPolicy.active(desired)) effects.start() else effects.stop()
            } catch (error: Exception) {
                // Roll back only fields owned here, retaining concurrent edits elsewhere.
                update { copyControls(it, before) }
                runCatching {
                    effects.configure(read())
                    if (GuardianPolicy.active(before)) effects.start() else effects.stop()
                }.onFailure {
                    update { it.copy(guardianEnabled = false, guardianPaused = false) }
                    runCatching { effects.configure(read()); effects.stop() }
                }
                throw error
            }
        }
    }

    private fun copyControls(current: WorkbenchSettings, source: WorkbenchSettings) = current.copy(
        notificationsEnabled = source.notificationsEnabled,
        guardianEnabled = source.guardianEnabled,
        guardianPaused = source.guardianPaused
    )
}
