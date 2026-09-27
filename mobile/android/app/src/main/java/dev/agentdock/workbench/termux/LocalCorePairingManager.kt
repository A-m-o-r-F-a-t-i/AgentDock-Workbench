package dev.agentdock.workbench.termux

import android.content.Context
import android.security.keystore.KeyGenParameterSpec
import android.security.keystore.KeyProperties
import android.util.Base64
import dev.agentdock.workbench.data.CredentialStore
import org.json.JSONObject
import java.net.URI
import java.security.KeyPairGenerator
import java.security.KeyStore
import java.security.PrivateKey
import java.security.spec.MGF1ParameterSpec
import javax.crypto.Cipher
import javax.crypto.spec.OAEPParameterSpec
import javax.crypto.spec.PSource

/** One-shot local credential pairing. Only the public key crosses RUN_COMMAND. */
class LocalCorePairingManager(
    context: Context,
    private val credentials: CredentialStore
) {
    private val preferences = context.getSharedPreferences(PREFERENCES, Context.MODE_PRIVATE)
    private val keyStore = KeyStore.getInstance("AndroidKeyStore").apply { load(null) }

    @Synchronized
    fun prepare(operationId: String, origin: URI): JSONObject {
        require(validOperationId(operationId))
        require(loopbackOrigin(origin)) { "本机 Core 自动配对只允许字面 loopback Origin" }
        cleanupExpired(System.currentTimeMillis())
        discard(operationId)
        val alias = alias(operationId)
        val generator = KeyPairGenerator.getInstance(KeyProperties.KEY_ALGORITHM_RSA, "AndroidKeyStore")
        generator.initialize(
            KeyGenParameterSpec.Builder(alias, KeyProperties.PURPOSE_DECRYPT)
                .setKeySize(2048)
                .setDigests(KeyProperties.DIGEST_SHA256, KeyProperties.DIGEST_SHA1)
                .setEncryptionPaddings(KeyProperties.ENCRYPTION_PADDING_RSA_OAEP)
                .setRandomizedEncryptionRequired(true)
                .build()
        )
        val pair = generator.generateKeyPair()
        preferences.edit()
            .putString("$operationId.origin", canonicalOrigin(origin))
            .putLong("$operationId.created", System.currentTimeMillis())
            .apply()
        return JSONObject()
            .put("key_id", operationId)
            .put("algorithm", ALGORITHM)
            .put("public_key_der", Base64.encodeToString(pair.public.encoded, Base64.NO_WRAP))
    }

    @Synchronized
    fun consume(operationId: String, data: JSONObject): URI {
        require(validOperationId(operationId))
        require(data.optString("key_id") == operationId && data.optString("algorithm") == ALGORITHM) {
            "配对密文与本地请求不匹配"
        }
        val encoded = data.optString("sealed_value")
        require(encoded.length in 128..2048 && encoded.matches(Regex("^[A-Za-z0-9+/=_-]+$"))) { "配对密文格式无效" }
        val originText = preferences.getString("$operationId.origin", null) ?: error("配对请求已过期或不属于此应用实例")
        val created = preferences.getLong("$operationId.created", 0L)
        require(System.currentTimeMillis() - created in 0..MAX_AGE_MS) { "配对请求已过期" }
        val privateKey = keyStore.getKey(alias(operationId), null) as? PrivateKey ?: error("配对私钥不可用")
        require(privateKey.algorithm.equals(KeyProperties.KEY_ALGORITHM_RSA, ignoreCase = true)) { "配对私钥算法无效" }
        val cipher = Cipher.getInstance(TRANSFORMATION)
        cipher.init(Cipher.DECRYPT_MODE, privateKey, OAEP_SPEC)
        val plaintext = cipher.doFinal(Base64.decode(encoded, Base64.DEFAULT)).toString(Charsets.UTF_8)
        require(plaintext.matches(Regex("^[A-Fa-f0-9]{64}$"))) { "解密后的本机凭据格式无效" }
        val origin = URI(originText)
        require(loopbackOrigin(origin) && canonicalOrigin(origin) == originText) { "配对 Origin 记录无效" }
        credentials.putCore(origin, plaintext)
        discard(operationId)
        return origin
    }

    @Synchronized
    fun discard(operationId: String) {
        if (!validOperationId(operationId)) return
        preferences.edit().remove("$operationId.origin").remove("$operationId.created").apply()
        if (keyStore.containsAlias(alias(operationId))) keyStore.deleteEntry(alias(operationId))
    }

    @Synchronized
    fun cleanupExpired(nowEpochMs: Long) {
        val ids = preferences.all.keys.mapNotNull { key -> key.removeSuffix(".created").takeIf { key.endsWith(".created") && validOperationId(it) } }.distinct()
        ids.forEach { id ->
            val created = preferences.getLong("$id.created", 0L)
            if (created <= 0L || nowEpochMs - created !in 0..MAX_AGE_MS) discard(id)
        }
    }

    private fun alias(operationId: String) = "$KEY_PREFIX$operationId"

    companion object {
        const val ALGORITHM = "RSA-OAEP-SHA256-MGF1-SHA1"
        const val MAX_AGE_MS = 2 * 60 * 60 * 1000L
        private const val TRANSFORMATION = "RSA/ECB/OAEPWithSHA-256AndMGF1Padding"
        private const val PREFERENCES = "agentdock_local_pairing"
        private const val KEY_PREFIX = "agentdock.workbench.local-pairing."
        private val OAEP_SPEC = OAEPParameterSpec("SHA-256", "MGF1", MGF1ParameterSpec.SHA1, PSource.PSpecified.DEFAULT)

        fun loopbackOrigin(origin: URI): Boolean {
            if (origin.path.orEmpty().isNotEmpty() || origin.query != null || origin.fragment != null || origin.userInfo != null) return false
            if (origin.scheme !in setOf("http", "https") || origin.port != -1 && origin.port !in 1..65535) return false
            val host = origin.host?.removeSurrounding("[", "]")?.lowercase() ?: return false
            return host in setOf("localhost", "127.0.0.1", "::1", "0:0:0:0:0:0:0:1")
        }

        fun canonicalOrigin(origin: URI): String {
            require(loopbackOrigin(origin))
            val defaultPort = when (origin.scheme) { "http" -> 80; else -> 443 }
            val port = if (origin.port < 0) defaultPort else origin.port
            val host = origin.host.removeSurrounding("[", "]").lowercase().let { if (':' in it) "[$it]" else it }
            return "${origin.scheme.lowercase()}://$host:$port"
        }

        private fun validOperationId(value: String) = Regex("^op_[A-Za-z0-9_-]{16,96}$").matches(value)
    }
}
