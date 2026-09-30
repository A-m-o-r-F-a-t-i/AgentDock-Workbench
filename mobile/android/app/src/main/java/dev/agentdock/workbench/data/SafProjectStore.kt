package dev.agentdock.workbench.data

import android.content.ContentResolver
import android.content.Context
import android.net.Uri
import android.provider.DocumentsContract
import java.io.File
import java.io.IOException
import java.io.ByteArrayOutputStream
import java.net.URLConnection
import java.util.Enumeration
import java.util.UUID
import java.util.zip.ZipEntry
import java.util.zip.ZipFile
import java.util.zip.ZipOutputStream

data class SafEntry(
    val name: String,
    val relativePath: String,
    val uri: String,
    val directory: Boolean,
    val sizeBytes: Long,
    val mimeType: String
)

data class SafTransferResult(
    val message: String,
    val projectName: String,
    val itemCount: Int,
    val bytes: Long,
    val backupRetained: Boolean = false
)

data class ArchivePlanEntry(val path: String, val directory: Boolean, val declaredSize: Long)

object ProjectArchivePolicy {
    const val MAX_ARCHIVE_BYTES = 256L * 1024 * 1024
    const val MAX_EXPANDED_BYTES = 512L * 1024 * 1024
    const val MAX_ENTRIES = 10_000
    const val MAX_FILE_BYTES = 128L * 1024 * 1024
    const val MAX_RELATIVE_PATH_CHARS = 4096
    const val MAX_DIRECTORY_DEPTH = 128

    fun requireFileSize(bytes: Long) {
        require(bytes in -1..MAX_FILE_BYTES) { "工程单文件超过可重新导入的大小上限" }
    }

    internal fun <T> collectBounded(entries: Enumeration<T>, maximum: Int = MAX_ENTRIES): List<T> {
        require(maximum in 1..MAX_ENTRIES) { "归档枚举上限无效" }
        val result = ArrayList<T>(minOf(maximum, 256))
        while (entries.hasMoreElements()) {
            require(result.size < maximum) { "归档文件数量超过上限" }
            result += entries.nextElement()
        }
        return result
    }

    internal fun requireDirectoryRow(rowNumber: Int) {
        require(rowNumber in 1..MAX_ENTRIES) { "文档目录条目超过上限" }
    }

    private fun validDocumentName(value: String): Boolean = value.length in 1..255 &&
        value !in setOf(".", "..") &&
        value.none { it == '/' || it == '\\' || it == '\u0000' || it.code < 32 }

    internal fun documentName(value: String): String {
        require(validDocumentName(value)) { "文档提供程序返回了无效名称" }
        return value
    }

    internal fun relativePath(prefix: String, name: String, depth: Int): String {
        require(depth in 1..MAX_DIRECTORY_DEPTH) { "工程目录深度超过上限" }
        val safeName = documentName(name)
        val result = if (prefix.isEmpty()) safeName else "$prefix/$safeName"
        require(result.length <= MAX_RELATIVE_PATH_CHARS) { "工程相对路径超过上限" }
        return result
    }

    internal fun registerDirectory(visited: MutableSet<String>, documentId: String) {
        require(documentId.isNotBlank()) { "文档提供程序返回了空目录标识" }
        require(visited.add(documentId)) { "文档目录包含循环或重复引用" }
    }

    fun validate(entries: List<ArchivePlanEntry>): List<ArchivePlanEntry> {
        require(entries.size <= MAX_ENTRIES) { "归档文件数量超过上限" }
        val normalized = entries.map { entry ->
            val raw = entry.path.removeSuffix("/")
            require(raw.isNotBlank() && raw.length <= MAX_RELATIVE_PATH_CHARS && !raw.startsWith('/') && '\\' !in raw && '\u0000' !in raw) { "归档路径无效" }
            val parts = raw.split('/')
            require(parts.size <= MAX_DIRECTORY_DEPTH && parts.all { part ->
                part.isNotBlank() && part !in setOf(".", "..") && part.length <= 255 && part.all { char -> char.code >= 32 }
            }) { "归档路径越界、层级过深或包含控制字符" }
            requireFileSize(entry.declaredSize)
            ArchivePlanEntry(parts.joinToString("/"), entry.directory, entry.declaredSize)
        }
        require(normalized.map { it.path }.distinct().size == normalized.size) { "归档包含重复路径" }
        val files = normalized.filterNot { it.directory }.map { it.path }.toSet()
        require(normalized.none { entry -> entry.path.split('/').dropLast(1).indices.any { index -> entry.path.split('/').take(index + 1).joinToString("/") in files } }) {
            "归档文件和子路径冲突"
        }
        val declared = normalized.filterNot { it.directory }.sumOf { it.declaredSize.coerceAtLeast(0) }
        require(declared <= MAX_EXPANDED_BYTES) { "归档声明的解包总量超过上限" }
        return normalized
    }

    fun projectName(value: String): String {
        val result = value.trim()
        require(result.length in 1..128 && validDocumentName(result)) { "工程名称无效" }
        return result
    }
}

class SafProjectStore(context: Context) {
    private val resolver: ContentResolver = context.contentResolver
    private val cache = File(context.cacheDir, "project-transfers").apply { mkdirs() }

    fun listProjects(treeText: String): List<SafEntry> {
        val tree = tree(treeText)
        return children(tree, root(tree)).filter { it.directory }.sortedBy { it.name.lowercase() }
    }

    fun listProject(treeText: String, projectName: String, maximum: Int = 500): List<SafEntry> {
        require(maximum in 1..2_000)
        val tree = tree(treeText)
        val project = findChild(tree, root(tree), ProjectArchivePolicy.projectName(projectName))
            ?: error("工程目录不存在")
        require(project.directory) { "所选对象不是工程目录" }
        val result = ArrayList<SafEntry>()
        val visitedDirectories = HashSet<String>()
        fun walk(parent: SafEntry, prefix: String, depth: Int) {
            val parentUri = Uri.parse(parent.uri)
            ProjectArchivePolicy.registerDirectory(visitedDirectories, DocumentsContract.getDocumentId(parentUri))
            for (child in children(tree, parentUri).sortedBy { it.name.lowercase() }) {
                check(result.size < maximum) { "工程文件超过页面上限，请导出后离线检查" }
                val relative = ProjectArchivePolicy.relativePath(prefix, child.name, depth + 1)
                val item = child.copy(relativePath = relative)
                result += item
                if (item.directory) walk(item, relative, depth + 1)
            }
        }
        walk(project, "", 0)
        return result
    }

    fun readText(treeText: String, projectName: String, relativePath: String, maximumBytes: Int = 100_000): String {
        require(maximumBytes in 1..1_000_000)
        val tree = tree(treeText)
        var current = findChild(tree, root(tree), ProjectArchivePolicy.projectName(projectName)) ?: error("工程不存在")
        for (part in safeRelative(relativePath)) {
            current = findChild(tree, Uri.parse(current.uri), part) ?: error("文件不存在")
        }
        require(!current.directory && current.sizeBytes in -1..maximumBytes.toLong()) { "文件不是可预览的有界文本" }
        (resolver.openInputStream(Uri.parse(current.uri)) ?: throw IOException("文档提供程序无法打开文件输入流")).use { input ->
            val output = ByteArrayOutputStream(minOf(maximumBytes, 8192))
            val buffer = ByteArray(8192)
            while (output.size() <= maximumBytes) {
                val read = input.read(buffer)
                if (read < 0) break
                output.write(buffer, 0, read)
            }
            val bytes = output.toByteArray()
            require(bytes.size <= maximumBytes) { "文件超过预览上限" }
            return bytes.toString(Charsets.UTF_8)
        }
    }

    fun importZip(treeText: String, source: Uri, requestedName: String, conflict: String): SafTransferResult {
        require(conflict in setOf("fail", "rename", "replace"))
        val tree = tree(treeText)
        val parent = root(tree)
        val initialName = ProjectArchivePolicy.projectName(requestedName)
        val existing = findChild(tree, parent, initialName)
        if (existing != null && conflict == "fail") error("同名工程已存在，未写入任何文件")
        val archive = File(cache, "${UUID.randomUUID()}.zip")
        try {
            copyBounded(source, archive, ProjectArchivePolicy.MAX_ARCHIVE_BYTES)
            ZipFile(archive).use { zip ->
                val sourceEntries = ProjectArchivePolicy.collectBounded(zip.entries())
                val plan = ProjectArchivePolicy.validate(sourceEntries.map { ArchivePlanEntry(it.name, it.isDirectory, it.size) })
                val byPath = sourceEntries.associateBy { it.name.removeSuffix("/").split('/').joinToString("/") }
                val stageName = "AgentDock Import ${UUID.randomUUID()}"
                val stage = create(tree, parent, stageName, true)
                var written = 0L
                var count = 0
                val directories = mutableMapOf("" to Uri.parse(stage.uri))
                try {
                    for (entry in plan.sortedWith(compareBy<ArchivePlanEntry>({ it.path.count { char -> char == '/' } }, { !it.directory }, { it.path }))) {
                        val parts = entry.path.split('/')
                        var directoryPath = ""
                        var parentUri = Uri.parse(stage.uri)
                        for (part in parts.dropLast(1)) {
                            directoryPath = if (directoryPath.isEmpty()) part else "$directoryPath/$part"
                            parentUri = directories.getOrPut(directoryPath) { create(tree, parentUri, part, true).let { Uri.parse(it.uri) } }
                        }
                        val name = parts.last()
                        if (entry.directory) {
                            directories[entry.path] = create(tree, parentUri, name, true).let { Uri.parse(it.uri) }
                            count++
                            continue
                        }
                        val target = create(tree, parentUri, name, false)
                        val sourceEntry = byPath[entry.path] ?: error("归档条目在校验后消失")
                        zip.getInputStream(sourceEntry).use { input ->
                            (resolver.openOutputStream(Uri.parse(target.uri), "w") ?: throw IOException("文档提供程序无法打开导入目标")).use { output ->
                                val buffer = ByteArray(64 * 1024)
                                var fileBytes = 0L
                                while (true) {
                                    val read = input.read(buffer)
                                    if (read < 0) break
                                    fileBytes += read
                                    written += read
                                    require(fileBytes <= ProjectArchivePolicy.MAX_FILE_BYTES && written <= ProjectArchivePolicy.MAX_EXPANDED_BYTES) { "实际解包量超过上限" }
                                    output.write(buffer, 0, read)
                                }
                            }
                        }
                        count++
                    }
                    var targetName = initialName
                    if (existing != null && conflict == "rename") targetName = availableName(tree, parent, initialName)
                    var backup: SafEntry? = null
                    if (existing != null && conflict == "replace") {
                        val backupName = "AgentDock Backup ${UUID.randomUUID()}"
                        backup = rename(existing, backupName)
                    }
                    val published = try {
                        rename(stage, targetName)
                    } catch (error: Exception) {
                        backup?.let { previous -> runCatching { rename(previous, initialName) } }
                        throw error
                    }
                    val backupRetained = backup?.let { previous -> !runCatching { delete(previous) }.getOrDefault(false) } ?: false
                    return SafTransferResult("导入完成并通过临时目录发布", published.name, count, written, backupRetained)
                } catch (error: Exception) {
                    runCatching { delete(stage) }
                    throw error
                }
            }
        } finally {
            archive.delete()
        }
    }

    fun exportZip(treeText: String, projectName: String, destination: Uri): SafTransferResult {
        val tree = tree(treeText)
        val project = findChild(tree, root(tree), ProjectArchivePolicy.projectName(projectName)) ?: error("工程不存在")
        require(project.directory)
        var count = 0
        var total = 0L
        val visitedDirectories = HashSet<String>()
        try {
            (resolver.openOutputStream(destination, "w") ?: throw IOException("文档提供程序无法打开导出目标")).use { raw ->
                ZipOutputStream(ArchiveOutputStream(raw.buffered())).use { zip ->
                    fun walk(parent: SafEntry, prefix: String, depth: Int) {
                        val parentUri = Uri.parse(parent.uri)
                        ProjectArchivePolicy.registerDirectory(visitedDirectories, DocumentsContract.getDocumentId(parentUri))
                        for (child in children(tree, parentUri).sortedBy { it.name.lowercase() }) {
                            require(++count <= ProjectArchivePolicy.MAX_ENTRIES) { "工程文件数量超过导出上限" }
                            val relative = ProjectArchivePolicy.relativePath(prefix, child.name, depth + 1)
                            if (child.directory) {
                                zip.putNextEntry(ZipEntry("$relative/").apply { time = 0 })
                                zip.closeEntry()
                                walk(child, relative, depth + 1)
                            } else {
                                ProjectArchivePolicy.requireFileSize(child.sizeBytes)
                                zip.putNextEntry(ZipEntry(relative).apply { time = 0 })
                                (resolver.openInputStream(Uri.parse(child.uri)) ?: throw IOException("文档提供程序无法读取工程文件")).use { input ->
                                    val buffer = ByteArray(64 * 1024)
                                    var fileBytes = 0L
                                    while (true) {
                                        val read = input.read(buffer)
                                        if (read < 0) break
                                        fileBytes += read
                                        ProjectArchivePolicy.requireFileSize(fileBytes)
                                        total += read
                                        require(total <= ProjectArchivePolicy.MAX_EXPANDED_BYTES) { "工程导出总量超过上限" }
                                        zip.write(buffer, 0, read)
                                    }
                                }
                                zip.closeEntry()
                            }
                        }
                    }
                    walk(project, "", 0)
                }
            }
            return SafTransferResult("工程已导出为有界 ZIP", projectName, count, total)
        } catch (error: Exception) {
            runCatching { DocumentsContract.deleteDocument(resolver, destination) }
            throw error
        }
    }

    private fun tree(value: String): Uri {
        require(value.isNotBlank()) { "尚未选择项目目录" }
        val uri = Uri.parse(value)
        require(DocumentsContract.isTreeUri(uri)) { "项目目录不是有效 SAF Tree URI" }
        return uri
    }

    private fun root(tree: Uri): Uri = DocumentsContract.buildDocumentUriUsingTree(tree, DocumentsContract.getTreeDocumentId(tree))

    private fun children(tree: Uri, parent: Uri): List<SafEntry> {
        val childUri = DocumentsContract.buildChildDocumentsUriUsingTree(tree, DocumentsContract.getDocumentId(parent))
        val projection = arrayOf(
            DocumentsContract.Document.COLUMN_DOCUMENT_ID,
            DocumentsContract.Document.COLUMN_DISPLAY_NAME,
            DocumentsContract.Document.COLUMN_MIME_TYPE,
            DocumentsContract.Document.COLUMN_SIZE
        )
        val result = ArrayList<SafEntry>()
        (resolver.query(childUri, projection, null, null, null) ?: throw IOException("文档提供程序无法列出目录")).use { cursor ->
            var rowNumber = 0
            while (cursor.moveToNext()) {
                ProjectArchivePolicy.requireDirectoryRow(++rowNumber)
                val id = cursor.getString(0) ?: throw IOException("文档提供程序返回了空文档标识")
                val name = ProjectArchivePolicy.documentName(
                    cursor.getString(1) ?: throw IOException("文档提供程序返回了空显示名称")
                )
                val mime = cursor.getString(2) ?: "application/octet-stream"
                val size = if (cursor.isNull(3)) -1L else cursor.getLong(3)
                val uri = DocumentsContract.buildDocumentUriUsingTree(tree, id)
                result += SafEntry(name, name, uri.toString(), mime == DocumentsContract.Document.MIME_TYPE_DIR, size, mime)
            }
        }
        return result
    }

    private fun findChild(tree: Uri, parent: Uri, name: String): SafEntry? = children(tree, parent).firstOrNull { it.name == name }

    private fun create(tree: Uri, parent: Uri, name: String, directory: Boolean): SafEntry {
        require(findChild(tree, parent, name) == null) { "目标路径已存在：$name" }
        val mime = if (directory) DocumentsContract.Document.MIME_TYPE_DIR else URLConnection.guessContentTypeFromName(name) ?: "application/octet-stream"
        val uri = DocumentsContract.createDocument(resolver, parent, mime, name) ?: error("文档提供程序拒绝创建：$name")
        val created = queryDocument(uri)
        if (created.name != name) {
            runCatching { DocumentsContract.deleteDocument(resolver, uri) }
            error("文档提供程序修改了目标名称，未继续写入")
        }
        return created
    }

    private fun rename(entry: SafEntry, name: String): SafEntry {
        ProjectArchivePolicy.projectName(name)
        val uri = DocumentsContract.renameDocument(resolver, Uri.parse(entry.uri), name) ?: error("文档提供程序不支持安全重命名")
        return queryDocument(uri)
    }

    private fun delete(entry: SafEntry): Boolean = DocumentsContract.deleteDocument(resolver, Uri.parse(entry.uri))

    private fun queryDocument(uri: Uri): SafEntry {
        val projection = arrayOf(
            DocumentsContract.Document.COLUMN_DISPLAY_NAME,
            DocumentsContract.Document.COLUMN_MIME_TYPE,
            DocumentsContract.Document.COLUMN_SIZE
        )
        (resolver.query(uri, projection, null, null, null) ?: throw IOException("文档提供程序无法回读文档")).use { cursor ->
            require(cursor.moveToFirst()) { "文档创建后无法回读" }
            val name = ProjectArchivePolicy.documentName(
                cursor.getString(0) ?: throw IOException("文档提供程序返回了空显示名称")
            )
            val mime = cursor.getString(1) ?: throw IOException("文档提供程序返回了空 MIME 类型")
            val size = if (cursor.isNull(2)) -1L else cursor.getLong(2)
            return SafEntry(name, name, uri.toString(), mime == DocumentsContract.Document.MIME_TYPE_DIR, size, mime)
        }
    }

    private fun availableName(tree: Uri, parent: Uri, requested: String): String {
        for (index in 2..999) {
            val suffix = " ($index)"
            val candidate = requested.take(128 - suffix.length) + suffix
            if (findChild(tree, parent, candidate) == null) return candidate
        }
        error("无法生成无冲突工程名称")
    }

    private fun safeRelative(value: String): List<String> {
        require(value.length in 1..ProjectArchivePolicy.MAX_RELATIVE_PATH_CHARS && !value.startsWith('/') && '\\' !in value)
        val parts = value.split('/')
        require(parts.size <= ProjectArchivePolicy.MAX_DIRECTORY_DEPTH)
        return parts.map(ProjectArchivePolicy::documentName)
    }

    private fun copyBounded(source: Uri, destination: File, maximum: Long) {
        (resolver.openInputStream(source) ?: throw IOException("文档提供程序无法打开导入 ZIP")).use { input ->
            destination.outputStream().use { output ->
                val buffer = ByteArray(64 * 1024)
                var total = 0L
                while (true) {
                    val read = input.read(buffer)
                    if (read < 0) break
                    total += read
                    if (total > maximum) throw IOException("ZIP 超过 ${maximum / 1024 / 1024} MiB 上限")
                    output.write(buffer, 0, read)
                }
                output.fd.sync()
            }
        }
    }
}
