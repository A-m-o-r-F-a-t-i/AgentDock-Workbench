package dev.agentdock.workbench.data

import java.io.IOException
import java.io.OutputStream

/** Counts compressed bytes including the ZIP central directory written by close(). */
class ArchiveOutputStream(private val output: OutputStream, private val maximum: Long = ProjectArchivePolicy.MAX_ARCHIVE_BYTES) : OutputStream() {
    var bytesWritten: Long = 0L
        private set
    init { require(maximum >= 0L) }

    override fun write(value: Int) {
        reserve(1)
        output.write(value)
        bytesWritten++
    }

    override fun write(value: ByteArray, offset: Int, length: Int) {
        require(offset >= 0 && length >= 0 && offset <= value.size - length)
        reserve(length)
        output.write(value, offset, length)
        bytesWritten += length
    }

    override fun flush() = output.flush()
    override fun close() = output.close()

    private fun reserve(length: Int) {
        if (length.toLong() > maximum - bytesWritten) throw IOException("ZIP 超过可重新导入的压缩包大小上限")
    }
}
