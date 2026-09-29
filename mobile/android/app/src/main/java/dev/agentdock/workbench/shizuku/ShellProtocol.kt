package dev.agentdock.workbench.shizuku

import org.json.JSONObject
import java.security.MessageDigest
import java.util.Base64

internal data class ShellSpec(val command: String, val workdir: String, val env: Map<String, String>, val timeoutMs: Long, val tty: Boolean) {
    fun fingerprint(): String {
        val values = org.json.JSONArray().put(command).put(workdir).put(timeoutMs).put(tty)
        env.toSortedMap().forEach { (k, v) -> values.put(k).put(v) }
        return MessageDigest.getInstance("SHA-256").digest(values.toString().toByteArray(Charsets.UTF_8)).joinToString("") { "%02x".format(it) }
    }
    fun environment(): Array<ByteArray> = (linkedMapOf("PATH" to "/system/bin:/system/xbin", "HOME" to "/",
        "TMPDIR" to "/data/local/tmp", "LANG" to "C.UTF-8", "TERM" to "xterm-256color") + env)
        .map { (k, v) -> "$k=$v".toByteArray(Charsets.UTF_8) }.toTypedArray()
}

internal object ShellProtocol {
    const val VERSION = 1
    const val MAX_REQUEST = 96 * 1024
    const val CHUNK = 16 * 1024
    val terminal = setOf("exited", "cancelled", "timeout", "not_started")
    fun identity(value: String): String = value.also { require(Regex("session-[a-f0-9]{24}").matches(it)) }
    fun node(value: String): String = value.also { require(it.length in 1..96 && it.none(Char::isISOControl)) }
    fun integer(json: JSONObject, key: String, minimum: Long, maximum: Long): Long {
        val value = json.opt(key)
        require(value is Int || value is Long) { "invalid_$key" }
        return (value as Number).toLong().also { require(it in minimum..maximum) { "invalid_$key" } }
    }
    fun spec(value: JSONObject): ShellSpec {
        require(value.keys().asSequence().all { it in setOf("backend", "command", "workdir", "env", "timeout_ms", "tty") })
        require(value.optString("backend") == "android_shizuku")
        val command = value.getString("command")
        require(command.toByteArray(Charsets.UTF_8).size in 1..16384 && '\u0000' !in command)
        val cwd = value.getString("workdir")
        require(cwd.startsWith('/') && cwd.toByteArray(Charsets.UTF_8).size <= 4096 && '\u0000' !in cwd)
        require(cwd == "/" || (!cwd.endsWith('/') && cwd.split('/').drop(1).none { it in setOf("", ".", "..") }))
        val envObject = value.optJSONObject("env") ?: JSONObject()
        require(!value.has("env") || value.opt("env") is JSONObject)
        require(envObject.length() <= 64)
        val env = envObject.keys().asSequence().associateWith { key ->
            require(Regex("[A-Za-z_][A-Za-z0-9_]{0,127}").matches(key))
            (envObject.get(key) as? String ?: error("invalid_environment")).also { require('\u0000' !in it) }
        }
        require(env.entries.sumOf { it.key.toByteArray().size + it.value.toByteArray().size } <= 16384)
        require(value.opt("tty") is Boolean)
        return ShellSpec(command, cwd, env, integer(value, "timeout_ms", 1, 86400000), value.getBoolean("tty"))
    }
    fun inputs(request: JSONObject): List<Pair<Long, ByteArray>> {
        val array = request.optJSONArray("input") ?: return emptyList<Pair<Long, ByteArray>>().also { require(!request.has("input")) }
        require(array.length() <= 4)
        val entries = (0 until array.length()).map { index ->
            val entry = array.getJSONObject(index)
            val seq = integer(entry, "sequence", 1, 1000000)
            val text = entry.getString("data")
            require(text.length <= 4 * ((CHUNK + 2) / 3))
            val data = Base64.getDecoder().decode(text)
            require(data.size <= CHUNK)
            seq to data
        }
        require(entries.map { it.first }.distinct().size == entries.size)
        return entries
    }
}
