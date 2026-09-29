package dev.agentdock.workbench.lifecycle

import android.Manifest
import android.content.Context
import android.content.Intent
import android.content.pm.PackageManager
import android.os.Build
import androidx.core.app.NotificationManagerCompat
import androidx.core.content.ContextCompat
import dev.agentdock.workbench.model.WorkbenchSettings

class AndroidGuardianEffects(private val context: Context) : GuardianEffects {
    override fun notificationsAllowed(): Boolean = NotificationManagerCompat.from(context).areNotificationsEnabled() &&
        (Build.VERSION.SDK_INT < 33 || ContextCompat.checkSelfPermission(context, Manifest.permission.POST_NOTIFICATIONS) == PackageManager.PERMISSION_GRANTED)
    override fun configure(settings: WorkbenchSettings) = GuardianScheduler.configure(context, settings)
    override fun start() { ContextCompat.startForegroundService(context, Intent(context, GuardianService::class.java)) }
    override fun stop() { context.stopService(Intent(context, GuardianService::class.java)) }
}
