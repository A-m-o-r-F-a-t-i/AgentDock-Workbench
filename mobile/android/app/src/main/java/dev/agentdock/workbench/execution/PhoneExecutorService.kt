package dev.agentdock.workbench.execution

import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.app.Service
import android.content.Intent
import android.os.IBinder
import androidx.core.app.NotificationCompat
import dev.agentdock.workbench.MainActivity
import dev.agentdock.workbench.R
import dev.agentdock.workbench.WorkbenchApplication
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.cancel
import kotlinx.coroutines.launch

/** Visible, user-started executor; it does not start or repair the Core itself. */
class PhoneExecutorService : Service() {
    private val scope = CoroutineScope(SupervisorJob() + Dispatchers.IO)
    private var loop: Job? = null
    override fun onBind(intent: Intent?): IBinder? = null
    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        val manager = getSystemService(NotificationManager::class.java)
        manager.createNotificationChannel(NotificationChannel(CHANNEL, "本机命令执行器", NotificationManager.IMPORTANCE_LOW))
        val open = PendingIntent.getActivity(this, 32, Intent(this, MainActivity::class.java), PendingIntent.FLAG_UPDATE_CURRENT or PendingIntent.FLAG_IMMUTABLE)
        startForeground(ID, NotificationCompat.Builder(this, CHANNEL).setSmallIcon(R.drawable.ic_stat_agentdock)
            .setContentTitle("AgentDock 本机执行器").setContentText("仅接受已配对本机 Core 的授权命令")
            .setContentIntent(open).setOngoing(true).setOnlyAlertOnce(true).build())
        if (loop?.isActive != true) loop = scope.launch {
            try { (application as WorkbenchApplication).graph.phoneExecutor.run() }
            finally { stopSelf() }
        }
        return START_STICKY
    }
    override fun onDestroy() { scope.cancel(); super.onDestroy() }
    companion object { const val CHANNEL = "agentdock_phone_executor"; const val ID = 11732 }
}
