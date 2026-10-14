package dev.agentdock.workbench.data

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test
import java.nio.file.Files

class PrivateCacheCleanerTest {
    @Test fun removesOnlyStaleOwnedTransferFiles() {
        val root = Files.createTempDirectory("agentdock-private-cache").toFile()
        try {
            val transfers = root.resolve("project-transfers").apply { mkdirs() }
            val staleExport = root.resolve("workbench-export-stale.json").apply { writeText("stale") }
            val freshExport = root.resolve("workbench-export-fresh.json").apply { writeText("fresh") }
            val staleArchive = transfers.resolve("01234567-89ab-cdef-0123-456789abcdef.zip").apply { writeText("archive") }
            val unrelated = root.resolve("unrelated.json").apply { writeText("keep") }
            val wrongArchive = transfers.resolve("not-a-transfer.zip").apply { writeText("keep") }
            val now = 10L * PrivateCacheCleaner.RETENTION_MS
            val stale = now - PrivateCacheCleaner.RETENTION_MS - 1
            assertTrue(staleExport.setLastModified(stale))
            assertTrue(staleArchive.setLastModified(stale))
            assertTrue(freshExport.setLastModified(now - 1))
            assertTrue(unrelated.setLastModified(stale))
            assertTrue(wrongArchive.setLastModified(stale))

            val result = PrivateCacheCleaner.cleanup(root, now)

            assertEquals(2, result.deleted)
            assertEquals(0, result.deferred)
            assertFalse(staleExport.exists())
            assertFalse(staleArchive.exists())
            assertTrue(freshExport.exists())
            assertTrue(unrelated.exists())
            assertTrue(wrongArchive.exists())
        } finally {
            root.deleteRecursively()
        }
    }
}
