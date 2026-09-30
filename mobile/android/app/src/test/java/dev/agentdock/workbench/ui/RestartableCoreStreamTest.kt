package dev.agentdock.workbench.ui

import dev.agentdock.workbench.data.SseMessage
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.awaitCancellation
import kotlinx.coroutines.test.runCurrent
import kotlinx.coroutines.test.runTest
import org.junit.Assert.*
import org.junit.Test

@OptIn(ExperimentalCoroutinesApi::class)
class RestartableCoreStreamTest {
    @Test fun completedStreamRestartsAtLastConfirmedCursor() = runTest {
        val stream = RestartableCoreStream(this)
        val cursors = mutableListOf<Long>()
        val observe: suspend (Long, suspend (SseMessage) -> Unit) -> Unit = { after, receive ->
            cursors += after
            receive(SseMessage("event", after + 1, "value"))
            receive(SseMessage("stopped", after + 1, "closed"))
        }
        stream.start(observe) {}; runCurrent()
        stream.start(observe) {}; runCurrent()
        assertEquals(listOf(0L, 1L), cursors)
    }

    @Test fun activeStreamIsNotDuplicated() = runTest {
        val stream = RestartableCoreStream(this)
        var starts = 0
        val observe: suspend (Long, suspend (SseMessage) -> Unit) -> Unit = { _, _ -> starts++; awaitCancellation() }
        stream.start(observe) {}; runCurrent()
        stream.start(observe) {}; runCurrent()
        assertEquals(1, starts)
        stream.stop(); runCurrent()
    }

    @Test fun oldCallbackCannotAffectNewNodeAndCursorResets() = runTest {
        val stream = RestartableCoreStream(this)
        var old: (suspend (SseMessage) -> Unit)? = null
        val received = mutableListOf<Long>()
        stream.start({ _, callback -> old = callback; callback(SseMessage("event", 9, "old")); awaitCancellation() }) { received += it.id }
        runCurrent(); stream.stop()
        var newCursor = -1L
        stream.start({ after, callback -> newCursor = after; callback(SseMessage("event", 1, "new")); awaitCancellation() }) { received += it.id }
        runCurrent()
        runCatching { old?.invoke(SseMessage("event", 999, "stale")) }
        assertEquals(0L, newCursor)
        assertEquals(listOf(9L, 1L), received)
        stream.stop(); runCurrent()
    }

    @Test fun resetEventAllowsLowerCursorOnReconnection() = runTest {
        val stream = RestartableCoreStream(this)
        stream.start({ _, callback ->
            callback(SseMessage("event", 100, "before"))
            callback(SseMessage("reset", 2, "reset"))
        }) {}
        runCurrent()
        var cursor = -1L
        stream.start({ after, _ -> cursor = after }) {}; runCurrent()
        assertEquals(2L, cursor)
    }

    @Test fun failedSubscriptionCanBeExplicitlyRetried() = runTest {
        val stream = RestartableCoreStream(this)
        val events = mutableListOf<String>()
        stream.start({ _, _ -> error("no credentials") }) { events += it.event }; runCurrent()
        stream.start({ _, callback -> callback(SseMessage("event", 1, "paired")) }) { events += it.event }; runCurrent()
        assertEquals(listOf("stopped", "event"), events)
    }
}
