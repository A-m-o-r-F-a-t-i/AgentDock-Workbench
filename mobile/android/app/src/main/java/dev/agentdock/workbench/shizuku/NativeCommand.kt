package dev.agentdock.workbench.shizuku

import java.io.File

/** Handles stay private to a single supervisor thread. No Android system API is emulated. */
object NativeCommand {
    const val RUNNING = -1000
    private var loaded = false
    @Synchronized fun initialize(directory: String? = null) {
        if (loaded) return
        val file = directory?.let { File(it, "libagentdock_exec.so") }
        if (file != null && file.isFile) System.load(file.absolutePath) else System.loadLibrary("agentdock_exec")
        loaded = true
    }
    @JvmStatic external fun spawn(command: ByteArray, workdir: ByteArray, environment: Array<ByteArray>, tty: Boolean): Long
    @JvmStatic external fun read(handle: Long, stream: Int): ByteArray?
    @JvmStatic external fun write(handle: Long, bytes: ByteArray, offset: Int): Int
    @JvmStatic external fun closeInput(handle: Long)
    @JvmStatic external fun poll(handle: Long): Int
    @JvmStatic external fun terminate(handle: Long)
    @JvmStatic external fun release(handle: Long)
}
