package dev.agentdock.workbench.data

import org.junit.Assert.assertEquals
import org.junit.Assert.assertThrows
import org.junit.Test
import java.util.Enumeration
import java.util.zip.ZipEntry

class ProjectArchivePolicyTest {
    @Test
    fun acceptsNormalizedFilesAndDirectories() {
        val result = ProjectArchivePolicy.validate(listOf(
            ArchivePlanEntry("src/", true, 0),
            ArchivePlanEntry("src/main.c", false, 128),
            ArchivePlanEntry("README.md", false, -1)
        ))
        assertEquals(listOf("src", "src/main.c", "README.md"), result.map { it.path })
    }

    @Test
    fun rejectsTraversalAbsoluteBackslashAndControlPaths() {
        listOf("../escape", "/absolute", "..\\escape", "a/../b", "a\u0000b").forEach { path ->
            assertThrows(IllegalArgumentException::class.java) {
                ProjectArchivePolicy.validate(listOf(ArchivePlanEntry(path, false, 1)))
            }
        }
    }

    @Test
    fun rejectsDuplicatesAndFileDirectoryConflicts() {
        assertThrows(IllegalArgumentException::class.java) {
            ProjectArchivePolicy.validate(listOf(
                ArchivePlanEntry("src/main.c", false, 1),
                ArchivePlanEntry("src/main.c", false, 2)
            ))
        }
        assertThrows(IllegalArgumentException::class.java) {
            ProjectArchivePolicy.validate(listOf(
                ArchivePlanEntry("src", false, 1),
                ArchivePlanEntry("src/main.c", false, 2)
            ))
        }
    }

    @Test
    fun rejectsDeclaredLimitsBeforeWriting() {
        assertThrows(IllegalArgumentException::class.java) {
            ProjectArchivePolicy.validate(listOf(ArchivePlanEntry("large.bin", false, ProjectArchivePolicy.MAX_FILE_BYTES + 1)))
        }
        val entries = List(ProjectArchivePolicy.MAX_ENTRIES + 1) { index -> ArchivePlanEntry("file-$index", false, 0) }
        assertThrows(IllegalArgumentException::class.java) { ProjectArchivePolicy.validate(entries) }
    }

    @Test
    fun boundedZipEnumerationRejectsBeforeMaterializingTheExtraEntry() {
        var index = 0
        var nextCalls = 0
        val entries = object : Enumeration<ZipEntry> {
            override fun hasMoreElements(): Boolean = index < 4
            override fun nextElement(): ZipEntry = ZipEntry("file-${index++}").also { nextCalls++ }
        }

        assertThrows(IllegalArgumentException::class.java) {
            ProjectArchivePolicy.collectBounded(entries, maximum = 3)
        }
        assertEquals(3, nextCalls)
    }

    @Test
    fun boundedZipEnumerationAcceptsTheExactLimit() {
        var index = 0
        val entries = object : Enumeration<ZipEntry> {
            override fun hasMoreElements(): Boolean = index < 3
            override fun nextElement(): ZipEntry = ZipEntry("file-${index++}")
        }

        assertEquals(3, ProjectArchivePolicy.collectBounded(entries, maximum = 3).size)
    }

    @Test
    fun directoryProviderRowsAreBoundedBeforeCollection() {
        ProjectArchivePolicy.requireDirectoryRow(ProjectArchivePolicy.MAX_ENTRIES)
        assertThrows(IllegalArgumentException::class.java) {
            ProjectArchivePolicy.requireDirectoryRow(ProjectArchivePolicy.MAX_ENTRIES + 1)
        }
    }

    @Test
    fun rejectsProviderNamesThatCouldCreateUnsafeZipEntries() {
        assertEquals("safe.txt", ProjectArchivePolicy.documentName("safe.txt"))
        listOf("", ".", "..", "../escape", "a/b", "a\\b", "a\u0000b", "a\u001fb", "x".repeat(256)).forEach { name ->
            assertThrows(IllegalArgumentException::class.java) { ProjectArchivePolicy.documentName(name) }
        }
    }

    @Test
    fun boundsProviderRelativePathsAndDirectoryDepth() {
        assertEquals("src/main.c", ProjectArchivePolicy.relativePath("src", "main.c", 2))
        assertThrows(IllegalArgumentException::class.java) {
            ProjectArchivePolicy.relativePath("src", "main.c", ProjectArchivePolicy.MAX_DIRECTORY_DEPTH + 1)
        }
        assertThrows(IllegalArgumentException::class.java) {
            ProjectArchivePolicy.relativePath("x".repeat(ProjectArchivePolicy.MAX_RELATIVE_PATH_CHARS), "main.c", 2)
        }
        val deep = (1..ProjectArchivePolicy.MAX_DIRECTORY_DEPTH + 1).joinToString("/") { "d$it" }
        assertThrows(IllegalArgumentException::class.java) {
            ProjectArchivePolicy.validate(listOf(ArchivePlanEntry(deep, true, 0)))
        }
    }

    @Test
    fun rejectsProviderDirectoryCyclesAndBlankIds() {
        val visited = mutableSetOf<String>()
        ProjectArchivePolicy.registerDirectory(visited, "root")
        ProjectArchivePolicy.registerDirectory(visited, "child")
        assertThrows(IllegalArgumentException::class.java) {
            ProjectArchivePolicy.registerDirectory(visited, "root")
        }
        assertThrows(IllegalArgumentException::class.java) {
            ProjectArchivePolicy.registerDirectory(visited, "")
        }
    }

    @Test
    fun projectNamesCannotEscapeOrContainControls() {
        assertEquals("Project A", ProjectArchivePolicy.projectName(" Project A "))
        listOf("", ".", "..", "a/b", "a\\b", "a\u0000b").forEach { name ->
            assertThrows(IllegalArgumentException::class.java) { ProjectArchivePolicy.projectName(name) }
        }
    }
}
