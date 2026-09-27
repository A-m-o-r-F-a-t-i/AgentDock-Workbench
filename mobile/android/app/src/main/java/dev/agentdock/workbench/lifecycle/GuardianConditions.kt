package dev.agentdock.workbench.lifecycle

import android.content.Context
import android.content.Intent
import android.content.IntentFilter
import android.net.ConnectivityManager
import android.net.NetworkCapabilities
import android.os.BatteryManager
import dev.agentdock.workbench.model.WorkbenchSettings

data class GuardianConditionSnapshot(val wifi: Boolean, val charging: Boolean)

object GuardianConstraintPolicy {
    fun waitReason(settings: WorkbenchSettings, snapshot: GuardianConditionSnapshot): String? = when {
        settings.onlyOnWifi && !snapshot.wifi -> "等待已验证的 Wi-Fi 连接"
        settings.onlyWhileCharging && !snapshot.charging -> "等待设备充电"
        else -> null
    }
}

object GuardianConditions {
    fun snapshot(context: Context): GuardianConditionSnapshot {
        val connectivity = context.getSystemService(ConnectivityManager::class.java)
        val capabilities = connectivity.activeNetwork?.let(connectivity::getNetworkCapabilities)
        val wifi = capabilities?.hasTransport(NetworkCapabilities.TRANSPORT_WIFI) == true &&
            capabilities.hasCapability(NetworkCapabilities.NET_CAPABILITY_VALIDATED)
        val battery = context.registerReceiver(null, IntentFilter(Intent.ACTION_BATTERY_CHANGED))
        val status = battery?.getIntExtra(BatteryManager.EXTRA_STATUS, -1) ?: -1
        val charging = status == BatteryManager.BATTERY_STATUS_CHARGING || status == BatteryManager.BATTERY_STATUS_FULL
        return GuardianConditionSnapshot(wifi, charging)
    }

    fun waitReason(context: Context, settings: WorkbenchSettings): String? =
        GuardianConstraintPolicy.waitReason(settings, snapshot(context))
}
