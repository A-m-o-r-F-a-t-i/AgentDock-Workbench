package dev.agentdock.workbench.lifecycle

import android.service.quicksettings.Tile
import android.service.quicksettings.TileService
import dev.agentdock.workbench.WorkbenchApplication
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.cancel
import kotlinx.coroutines.launch
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.withContext

class AgentDockTileService : TileService() {
    private val scope = CoroutineScope(Job() + Dispatchers.Main.immediate)
    private var failureMessage: String? = null

    override fun onStartListening() {
        super.onStartListening()
        refresh()
    }

    override fun onClick() {
        super.onClick()
        scope.launch {
            try {
                (application as WorkbenchApplication).graph.guardian.toggle()
                failureMessage = null
            } catch (error: CancellationException) { throw error
            } catch (_: Exception) { failureMessage = "未启用，请检查通知权限" }
            refresh()
        }
    }

    override fun onDestroy() {
        scope.cancel()
        super.onDestroy()
    }

    private fun refresh() {
        scope.launch {
            try {
                val settings = (application as WorkbenchApplication).graph.settings.current()
                qsTile?.apply {
                state = if (settings.guardianEnabled && !settings.guardianPaused) Tile.STATE_ACTIVE else Tile.STATE_INACTIVE
                if (android.os.Build.VERSION.SDK_INT >= 29) {
                    subtitle = failureMessage ?: when {
                        !settings.guardianEnabled -> "未启用"
                        settings.guardianPaused -> "守护已暂停"
                        else -> "守护已启用"
                    }
                }
                updateTile()
                }
            } catch (error: CancellationException) { throw error
            } catch (_: Exception) { /* Keep the last rendered tile when state cannot be read. */ }
        }
    }
}
