package dev.agentdock.workbench

import android.content.Context
import android.content.ContextWrapper
import androidx.test.ext.junit.runners.AndroidJUnit4
import androidx.test.platform.app.InstrumentationRegistry
import dev.agentdock.workbench.model.BridgeOperation
import dev.agentdock.workbench.termux.PendingOperationStore
import dev.agentdock.workbench.termux.TermuxResultPolicy
import org.json.JSONObject
import org.junit.Assert.*
import org.junit.Test
import org.junit.runner.RunWith
import java.io.File
import java.util.UUID

@RunWith(AndroidJUnit4::class)
class PendingOperationStoreTest {
    private fun withStore(clock: () -> Long = System::currentTimeMillis, block: (PendingOperationStore) -> Unit) {
        val base = InstrumentationRegistry.getInstrumentation().targetContext
        val directory = File(base.cacheDir, "operations-test-${UUID.randomUUID()}").apply { check(mkdirs()) }
        val context = object : ContextWrapper(base) { override fun getFilesDir(): File = directory }
        try { block(PendingOperationStore(context, clock)) } finally { directory.deleteRecursively() }
    }
    private fun operation(index: Int) = BridgeOperation(
        operationId = "op_$index", requestId = "req_$index", nonce = "abcdefghijklmnopqrstuvwxyzABCDE123456",
        operation = "install", createdAtEpochMs = System.currentTimeMillis()
    )
    @Test fun lateDispatchCannotReplaceAnAlreadyCompletedCallback() = withStore { store ->
        val request = operation(1); store.create(request)
        store.finish(request, "succeeded", "callback complete", 0, false, false)
        val after = store.finish(request, "running", "late dispatch", null, false, false)
        assertEquals("succeeded", after.phase); assertEquals("callback complete", after.message)
        assertEquals(0, after.exitCode)
    }
    @Test fun unresolvedOperationsAreNotEvictedToMakeRoom() = withStore { store ->
        repeat(128) { store.create(operation(it)) }
        assertThrows(IllegalStateException::class.java) { store.create(operation(129)) }
        assertNotNull(store.get("op_0"))
        val request = checkNotNull(store.get("op_0"))
        store.finish(request, "failed", "terminal", 1, false, false)
        store.create(operation(129))
        assertNull(store.get("op_0")); assertNotNull(store.get("op_129"))
    }

    @Test fun recoveryQueryRetainsCapacityWhenNormalOperationsAreFull() = withStore { store ->
        repeat(128) { store.create(operation(it)) }
        val query = operation(200).copy(operation = "operation_query", targetOperationId = "op_0")
        store.create(query)
        assertNotNull(store.get(query.operationId))
        assertThrows(IllegalStateException::class.java) { store.create(operation(201)) }
        val data = JSONObject().put("operation_id", "op_0").put("operation", "install")
            .put("status", "succeeded").put("message", "verified original")
        store.finish(query, "succeeded", "query complete", 0, false, false, data.toString())
        assertTrue(store.reconcile(query, data))
        assertEquals("succeeded", store.get("op_0")?.phase)
        store.create(operation(201))
        assertNull(store.get("op_0"))
    }

    @Test fun expiryIsUnknownAndCanBeSettledByOriginalId() {
        var now = System.currentTimeMillis()
        withStore({ now }) { store ->
            val original = operation(1).copy(createdAtEpochMs = now)
            store.create(original)
            now += TermuxResultPolicy.MAX_CALLBACK_AGE_MS
            assertEquals("queued", store.list().single().phase)
            now++
            assertEquals("unknown", store.list().single().phase)
            assertNull(store.get(original.operationId)?.exitCode)
            val query = operation(2).copy(operation = "operation_query", targetOperationId = original.operationId, createdAtEpochMs = now)
            store.create(query)
            val data = JSONObject().put("operation_id", original.operationId).put("operation", original.operation).put("status", "succeeded")
            store.finish(query, "succeeded", "confirmed", 0, false, false, data.toString())
            assertTrue(store.reconcile(query, data))
            assertFalse(store.reconcile(query, data))
            assertEquals("succeeded", store.get(original.operationId)?.phase)
        }
    }

    @Test fun queryCannotReconcileDifferentOriginalOrOperation() = withStore { store ->
        val original = operation(1); store.create(original)
        val query = operation(2).copy(operation = "operation_query", targetOperationId = original.operationId)
        store.create(query); store.finish(query, "succeeded", "query", 0, false, false)
        val data = JSONObject().put("operation_id", "other").put("operation", "install").put("status", "succeeded")
        assertThrows(IllegalArgumentException::class.java) { store.reconcile(query, data) }
        data.put("operation_id", original.operationId).put("operation", "stop")
        assertThrows(IllegalArgumentException::class.java) { store.reconcile(query, data) }
        assertEquals("queued", store.get(original.operationId)?.phase)
    }

    @Test fun continuationCanSettleBlockedOriginalWithoutReplayingIt() = withStore { store ->
        val original = operation(1); store.create(original)
        store.finish(original, "pending_manifest", "await trust", 0, false, false)
        val resume = operation(2).copy(operation = "resume", targetOperationId = original.operationId)
        store.create(resume); store.finish(resume, "succeeded", "resumed", 0, false, false)
        val data = JSONObject().put("operation_id", original.operationId).put("operation", "install").put("status", "succeeded")
        assertTrue(store.reconcile(resume, data))
        assertEquals("succeeded", store.get(original.operationId)?.phase)
    }

    @Test fun restartPreservesOriginalQueryBinding() {
        val base = InstrumentationRegistry.getInstrumentation().targetContext
        val directory = File(base.cacheDir, "query-restart-${UUID.randomUUID()}").apply { check(mkdirs()) }
        val context = object : ContextWrapper(base) { override fun getFilesDir(): File = directory }
        try {
            val query = operation(1).copy(operation = "operation_query", intentRevision = 42, targetOperationId = "op_original")
            PendingOperationStore(context).create(query)
            val restored = checkNotNull(PendingOperationStore(context).get(query.operationId))
            assertEquals(42L, restored.intentRevision); assertEquals("op_original", restored.targetOperationId)
            val file = File(directory, "operations/${query.operationId}.json")
            val legacy = JSONObject(file.readText()).apply { remove("intent_revision"); remove("target_operation_id") }
            file.writeText(legacy.toString())
            val migrated = checkNotNull(PendingOperationStore(context).get(query.operationId))
            assertEquals(-1L, migrated.intentRevision); assertEquals("", migrated.targetOperationId)
        } finally { directory.deleteRecursively() }
    }

    @Test fun blockedDeploymentsAreNotEvictedAsTransportTerminal() = withStore { store ->
        repeat(128) {
            val request = operation(it); store.create(request)
            store.finish(request, "pending_manifest", "needs trust", 0, false, false)
        }
        assertThrows(IllegalStateException::class.java) { store.create(operation(129)) }
        store.create(operation(200).copy(operation = "operation_query", targetOperationId = "op_0"))
        assertEquals("pending_manifest", store.get("op_0")?.phase)
    }
}
