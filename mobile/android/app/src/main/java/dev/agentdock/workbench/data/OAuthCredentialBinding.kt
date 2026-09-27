package dev.agentdock.workbench.data

import org.json.JSONObject
import java.net.URI

data class OAuthCredentialStatus(
    val configured: Boolean,
    val valid: Boolean,
    val expiresAtEpochMs: Long,
    val clientId: String,
    val issuer: String
)

object OAuthCredentialBinding {
    private const val MAX_ACCESS_CHARS = 16_384
    private const val MAX_RECORD_CHARS = 32_768

    fun encode(origin: URI, accessToken: String, expiresAtEpochMs: Long, clientId: String, issuer: String): String {
        require(validToken(accessToken) && expiresAtEpochMs > System.currentTimeMillis())
        require(clientId.length in 1..512 && issuer.length in 1..4096)
        val value = JSONObject()
            .put("schema_version", 1)
            .put("origin", CoreCredentialBinding.originKey(origin))
            .put("access_token", accessToken)
            .put("expires_at_epoch_ms", expiresAtEpochMs)
            .put("client_id", clientId)
            .put("issuer", issuer)
            .toString()
        require(value.length <= MAX_RECORD_CHARS)
        return value
    }

    fun access(value: String, origin: URI, nowEpochMs: Long = System.currentTimeMillis()): String {
        val record = decode(value, origin) ?: return ""
        val expires = record.optLong("expires_at_epoch_ms", 0L)
        if (expires - nowEpochMs <= 30_000L) return ""
        val token = record.optString("access_token")
        return token.takeIf(::validToken).orEmpty()
    }

    fun status(value: String, origin: URI, nowEpochMs: Long = System.currentTimeMillis()): OAuthCredentialStatus {
        val record = decode(value, origin) ?: return OAuthCredentialStatus(false, false, 0L, "", "")
        val expires = record.optLong("expires_at_epoch_ms", 0L)
        return OAuthCredentialStatus(
            configured = true,
            valid = expires - nowEpochMs > 30_000L && validToken(record.optString("access_token")),
            expiresAtEpochMs = expires,
            clientId = record.optString("client_id").take(512),
            issuer = record.optString("issuer").take(4096)
        )
    }

    private fun decode(value: String, origin: URI): JSONObject? {
        if (value.isBlank() || value.length > MAX_RECORD_CHARS) return null
        val record = runCatching { JSONObject(value) }.getOrNull() ?: return null
        if (record.opt("schema_version") != 1 || record.optString("origin") != CoreCredentialBinding.originKey(origin)) return null
        return record
    }

    private fun validToken(value: String): Boolean = value.length in 1..MAX_ACCESS_CHARS &&
        value.all { it.code in 33..126 }
}
