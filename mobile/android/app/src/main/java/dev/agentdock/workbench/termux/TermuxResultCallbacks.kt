package dev.agentdock.workbench.termux

import android.app.PendingIntent
import android.content.Context
import android.content.Intent
import android.os.Build
import dev.agentdock.workbench.model.BridgeOperation

/** Reconstructible callback identity survives APK process recreation. */
object TermuxResultCallbacks {
    private val mutableFlag: Int get() = if (Build.VERSION.SDK_INT >= 31) PendingIntent.FLAG_MUTABLE else 0

    fun create(context: Context, operation: BridgeOperation): PendingIntent = PendingIntent.getService(
        context, 0, intent(context, operation), PendingIntent.FLAG_UPDATE_CURRENT or mutableFlag
    )

    fun release(context: Context, operation: BridgeOperation) {
        PendingIntent.getService(context, 0, intent(context, operation), PendingIntent.FLAG_NO_CREATE or mutableFlag)?.cancel()
    }

    private fun intent(context: Context, operation: BridgeOperation) = Intent(context, TermuxResultService::class.java)
        .setAction(TermuxContract.CALLBACK_ACTION_PREFIX + operation.requestId)
        .putExtra(TermuxResultService.EXTRA_OPERATION_ID, operation.operationId)
        .putExtra(TermuxResultService.EXTRA_REQUEST_ID, operation.requestId)
        .putExtra(TermuxResultService.EXTRA_NONCE, operation.nonce)
}
