package dev.agentdock.workbench

import android.app.PendingIntent
import android.content.Intent
import android.os.Bundle
import android.os.SystemClock
import androidx.test.core.app.ActivityScenario
import androidx.test.ext.junit.runners.AndroidJUnit4
import androidx.test.platform.app.InstrumentationRegistry
import dev.agentdock.workbench.model.BridgeOperation
import dev.agentdock.workbench.termux.TermuxResultCallbacks
import org.json.JSONObject
import org.junit.Assert.*
import org.junit.Test
import org.junit.runner.RunWith
import java.util.UUID

/** Exercises Android's real PendingIntent and result Service, without installing Termux. */
@RunWith(AndroidJUnit4::class)
class TermuxCallbackServiceTest {
    @Test fun acknowledgementsDoNotConsumeTheFinalCallback() {
        val context = InstrumentationRegistry.getInstrumentation().targetContext
        ActivityScenario.launch<MainActivity>(Intent(context, MainActivity::class.java)
            .addFlags(Intent.FLAG_ACTIVITY_NEW_TASK).putExtra(MainActivity.EXTRA_FIXTURE, true)).use {
            val app = context.applicationContext as WorkbenchApplication
            val id = UUID.randomUUID().toString().replace("-", "")
            val operation = BridgeOperation(operationId = "op_$id", requestId = "req_$id",
                nonce = "callback-test-$id", operation = "probe", createdAtEpochMs = System.currentTimeMillis())
            app.graph.operations.create(operation)
            val callback = TermuxResultCallbacks.create(context, operation)
            assertEquals(callback, TermuxResultCallbacks.create(context, operation))
            callback.send(context, 0, Intent())
            callback.send(context, 0, Intent().putExtra("result", Bundle().apply {
                putInt("err", -1); putInt("exitCode", 0); putString("stdout", ""); putString("stderr", "")
            }))
            val json = JSONObject().put("schema_version", 1).put("operation_id", operation.operationId)
                .put("request_id", operation.requestId).put("nonce", operation.nonce)
                .put("operation", operation.operation).put("status", "healthy").put("message", "ok").toString()
            callback.send(context, 0, Intent().putExtra("result", Bundle().apply {
                putInt("err", -1); putInt("exitCode", 0); putString("stdout", json); putString("stderr", "")
                putString("stdout_original_length", json.length.toString()); putString("stderr_original_length", "0")
            }))
            val deadline = SystemClock.elapsedRealtime() + 10000
            while (app.graph.operations.get(operation.operationId)?.phase == "queued" && SystemClock.elapsedRealtime() < deadline) Thread.sleep(25)
            assertEquals("succeeded", app.graph.operations.get(operation.operationId)?.phase)
            assertThrows(PendingIntent.CanceledException::class.java) { callback.send() }
        }
    }
}
