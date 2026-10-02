package dev.agentdock.workbench.execution

import org.json.JSONObject

/** Allowlisted connection metadata; never accept raw requests, errors or leases. */
object PhoneDiagnostics {
    fun snapshot(version: String, sourceSha: String, enabled: Boolean, connected: Boolean, capabilities: JSONObject): JSONObject {
        val channels = JSONObject()
        for (name in listOf("termux_host", "android_shizuku")) {
            val raw = capabilities.optJSONObject(name) ?: JSONObject()
            val state = raw.optString("state").takeIf { it.matches(Regex("[a-z_]{1,80}")) } ?: "unknown"
            val value = JSONObject().put("state", state).put("ready", raw.opt("ready") == true)
            val uid = raw.opt("uid")
            if (uid is Int && uid >= 0) value.put("uid", uid)
            channels.put(name, value)
        }
        return JSONObject().put("schema_version", 1).put("apk_version", version.take(80))
            .put("source_sha", sourceSha.takeIf { it.matches(Regex("[a-fA-F0-9]{7,64}")) } ?: "local")
            .put("enabled", enabled).put("connected", connected).put("backends", channels)
    }
}
