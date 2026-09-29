package dev.agentdock.workbench.data

import java.io.ByteArrayOutputStream
import java.io.IOException
import java.util.zip.ZipEntry
import java.util.zip.ZipInputStream
import java.util.zip.ZipOutputStream
import org.junit.Assert.*
import org.junit.Test

class ArchiveOutputStreamTest {
    @Test fun exactCompressedLimitAcceptsAndNextByteIsRejected() {
        val target = ByteArrayOutputStream()
        val bounded = ArchiveOutputStream(target, 4)
        bounded.write(byteArrayOf(1, 2, 3), 0, 3); bounded.write(4)
        assertEquals(4L, bounded.bytesWritten)
        assertThrows(IOException::class.java) { bounded.write(5) }
        assertEquals(4, target.size())
    }

    @Test fun bulkOverflowDoesNotPartiallyWrite() {
        val target = ByteArrayOutputStream()
        val bounded = ArchiveOutputStream(target, 2)
        assertThrows(IOException::class.java) { bounded.write(byteArrayOf(1, 2, 3)) }
        assertEquals(0, target.size()); assertEquals(0L, bounded.bytesWritten)
    }

    @Test fun closeCentralDirectoryIsCountedAndCanRejectAnOversizedZip() {
        val target = ByteArrayOutputStream()
        val zip = ZipOutputStream(ArchiveOutputStream(target, 100))
        zip.putNextEntry(ZipEntry("file")); zip.write(1); zip.closeEntry()
        assertTrue(target.size() < 100)
        assertThrows(IOException::class.java) { zip.close() }
        assertTrue(target.size() <= 100)
    }

    @Test fun boundedZipRoundTripsThroughTheImportPolicy() {
        val target = ByteArrayOutputStream()
        val payload = "bounded project data".toByteArray()
        ZipOutputStream(ArchiveOutputStream(target, 4096)).use {
            it.putNextEntry(ZipEntry("file.txt")); it.write(payload); it.closeEntry()
        }
        ZipInputStream(target.toByteArray().inputStream()).use {
            val entry = it.nextEntry
            val data = it.readBytes()
            assertArrayEquals(payload, data)
            val plan = ProjectArchivePolicy.validate(listOf(ArchivePlanEntry(entry.name, false, data.size.toLong())))
            assertEquals("file.txt", plan.single().path)
            assertNull(it.nextEntry)
        }
    }

    @Test fun importAndExportSharePerFileBoundary() {
        val limit = ProjectArchivePolicy.MAX_FILE_BYTES
        ProjectArchivePolicy.requireFileSize(limit)
        ProjectArchivePolicy.validate(listOf(ArchivePlanEntry("exact", false, limit)))
        assertThrows(IllegalArgumentException::class.java) { ProjectArchivePolicy.requireFileSize(limit + 1) }
        assertThrows(IllegalArgumentException::class.java) {
            ProjectArchivePolicy.validate(listOf(ArchivePlanEntry("too-large", false, limit + 1)))
        }
    }
}
