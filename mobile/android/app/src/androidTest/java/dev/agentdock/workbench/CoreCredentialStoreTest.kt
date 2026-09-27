package dev.agentdock.workbench

import android.content.Context
import android.content.ContextWrapper
import android.content.SharedPreferences
import android.util.Base64
import androidx.test.ext.junit.runners.AndroidJUnit4
import androidx.test.platform.app.InstrumentationRegistry
import dev.agentdock.workbench.data.CredentialStore
import dev.agentdock.workbench.termux.LocalCorePairingManager
import org.json.JSONObject
import org.junit.Assert.*
import org.junit.Test
import org.junit.runner.RunWith
import java.net.URI
import java.security.KeyFactory
import java.security.spec.MGF1ParameterSpec
import java.security.spec.X509EncodedKeySpec
import java.util.UUID
import javax.crypto.Cipher
import javax.crypto.spec.OAEPParameterSpec
import javax.crypto.spec.PSource

@RunWith(AndroidJUnit4::class)
class CoreCredentialStoreTest {
    private fun withStore(block: (CredentialStore, SharedPreferences) -> Unit) {
        val base = InstrumentationRegistry.getInstrumentation().targetContext
        val name = "credential-test-${UUID.randomUUID()}"
        val preferences = base.getSharedPreferences(name, Context.MODE_PRIVATE)
        val context = object : ContextWrapper(base) {
            override fun getSharedPreferences(ignored: String, mode: Int): SharedPreferences = preferences
        }
        try { block(CredentialStore(context), preferences) }
        finally { check(preferences.edit().clear().commit()) }
    }
    @Test fun keystoreEnvelopeBindsTheCredentialToItsOrigin() = withStore { store, preferences ->
        val original = URI("https://first.example")
        val other = URI("https://second.example")
        val bearer = "fixture-keystore-bearer-not-production"
        store.putCore(original, bearer)
        assertEquals(bearer, store.getCore(original))
        assertEquals("", store.getCore(other))
        assertTrue(preferences.all.isNotEmpty())
        assertFalse(preferences.all.values.any { it.toString().contains(bearer) || it.toString().contains("first.example") })
        store.putCore(other, "fixture-replacement")
        assertEquals("", store.getCore(original))
        assertEquals("fixture-replacement", store.getCore(other))
    }
    @Test fun preservesButDoesNotSendAnUnboundLegacyCredential() = withStore { store, _ ->
        store.put("core_bearer", "fixture-unbound-legacy")
        assertEquals("", store.getCore(URI("http://127.0.0.1:8765")))
        assertEquals("fixture-unbound-legacy", store.get("core_bearer"))
        store.clear("core_bearer")
        assertEquals("", store.getCore(URI("http://127.0.0.1:8765")))
    }

    @Test fun oneShotPairingDecryptsOnlyForTheBoundLoopbackOrigin() {
        val base = InstrumentationRegistry.getInstrumentation().targetContext
        val preferences = base.getSharedPreferences("pairing-test-${UUID.randomUUID()}", Context.MODE_PRIVATE)
        val context = object : ContextWrapper(base) {
            override fun getSharedPreferences(ignored: String, mode: Int): SharedPreferences = preferences
        }
        val store = CredentialStore(context)
        val pairing = LocalCorePairingManager(context, store)
        val operationId = "op_${UUID.randomUUID().toString().replace("-", "")}"
        val origin = URI("http://127.0.0.1:8765")
        val token = "a".repeat(64)
        try {
            val request = pairing.prepare(operationId, origin)
            val publicKey = KeyFactory.getInstance("RSA").generatePublic(
                X509EncodedKeySpec(Base64.decode(request.getString("public_key_der"), Base64.DEFAULT))
            )
            val cipher = Cipher.getInstance("RSA/ECB/OAEPWithSHA-256AndMGF1Padding")
            cipher.init(
                Cipher.ENCRYPT_MODE,
                publicKey,
                OAEPParameterSpec("SHA-256", "MGF1", MGF1ParameterSpec.SHA1, PSource.PSpecified.DEFAULT)
            )
            val sealed = Base64.encodeToString(cipher.doFinal(token.toByteArray()), Base64.NO_WRAP)
            val pairedOrigin = pairing.consume(operationId, JSONObject()
                .put("key_id", operationId)
                .put("algorithm", LocalCorePairingManager.ALGORITHM)
                .put("sealed_value", sealed))
            assertEquals(origin, pairedOrigin)
            assertEquals(token, store.getCore(origin))
            assertEquals("", store.getCore(URI("http://127.0.0.1:8766")))
            assertFalse(preferences.all.values.any { it.toString().contains(token) || it.toString().contains(sealed) })
        } finally {
            pairing.discard(operationId)
            preferences.edit().clear().commit()
        }
    }
}
