package dev.agentdock.workbench.data

import java.io.File
import java.nio.file.Files

internal data class PrivateCacheCleanupResult(val deleted: Int, val deferred: Int)

/** Reclaim only AgentDock-owned cache files left by an interrupted transfer. */
internal object PrivateCacheCleaner {
    const val RETENTION_MS = 24 * 60 * 60 * 1000L
    private const val MAX_FILES_PER_RUN = 512
    private val projectArchive = Regex("^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}\\.zip$")

    fun cleanup(cacheRoot: File, nowEpochMs: Long = System.currentTimeMillis()): PrivateCacheCleanupResult {
        return runCatching {
            val cutoff = nowEpochMs - RETENTION_MS
            val candidates = buildList {
                cacheRoot.listFiles().orEmpty().filterTo(this) { file ->
                    file.name.startsWith("workbench-export-") && file.name.endsWith(".json")
                }
                val transfers = File(cacheRoot, "project-transfers")
                if (transfers.isDirectory && !Files.isSymbolicLink(transfers.toPath())) {
                    transfers.listFiles().orEmpty().filterTo(this) { projectArchive.matches(it.name) }
                }
            }.filter { file ->
                runCatching {
                    file.isFile && !Files.isSymbolicLink(file.toPath()) && file.lastModified() in 1L..cutoff
                }.getOrDefault(false)
            }.sortedBy(File::lastModified)
            var deleted = 0
            candidates.take(MAX_FILES_PER_RUN).forEach { file ->
                if (runCatching { file.delete() }.getOrDefault(false)) deleted++
            }
            PrivateCacheCleanupResult(deleted, (candidates.size - MAX_FILES_PER_RUN).coerceAtLeast(0))
        }.getOrDefault(PrivateCacheCleanupResult(0, 0))
    }
}
