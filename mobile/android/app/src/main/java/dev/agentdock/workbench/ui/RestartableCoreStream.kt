package dev.agentdock.workbench.ui

import dev.agentdock.workbench.data.SseMessage
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.CoroutineStart
import kotlinx.coroutines.Job
import kotlinx.coroutines.currentCoroutineContext
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext

/** Disposable client subscription, owned and called on its presentation scope. */
class RestartableCoreStream(private val scope: CoroutineScope) {
    private var job: Job? = null
    private var generation = 0L
    private var cursor = 0L

    fun start(observe: suspend (Long, suspend (SseMessage) -> Unit) -> Unit, receive: (SseMessage) -> Unit) {
        if (job?.isActive == true) return
        val current = ++generation
        val next = scope.launch(start = CoroutineStart.LAZY) {
            val delivery = currentCoroutineContext()
            try {
                observe(cursor) { event ->
                    withContext(delivery) {
                        if (current == generation) {
                            if (event.event == "reset") cursor = event.id.coerceAtLeast(0)
                            else if (event.event !in setOf("disconnected", "stopped")) cursor = maxOf(cursor, event.id)
                            receive(event)
                        }
                    }
                }
            } catch (error: CancellationException) {
                throw error
            } catch (_: Exception) {
                if (current == generation) receive(SseMessage("stopped", cursor, "活动流已停止，请刷新连接"))
            } finally {
                if (current == generation) job = null
            }
        }
        job = next
        next.start()
    }

    fun stop() {
        generation++
        job?.cancel()
        job = null
        cursor = 0L
    }
}
