package dev.agentdock.workbench.data

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test
import java.net.URI

class OAuthCredentialBindingTest {
    @Test
    fun tokenIsBoundToOriginAndAbsoluteExpiry() {
        val origin = URI("https://core.example:9443")
        val now = System.currentTimeMillis()
        val record = OAuthCredentialBinding.encode(origin, "oauth-access-token", now + 120_000, "client-id", origin.toString())
        assertEquals("oauth-access-token", OAuthCredentialBinding.access(record, origin, now))
        assertEquals("", OAuthCredentialBinding.access(record, URI("https://core.example:9444"), now))
        assertEquals("", OAuthCredentialBinding.access(record, origin, now + 100_000))
        val status = OAuthCredentialBinding.status(record, origin, now)
        assertTrue(status.configured)
        assertTrue(status.valid)
        assertEquals("client-id", status.clientId)
    }

    @Test
    fun expiredRecordRemainsInspectableButIsNeverSent() {
        val origin = URI("https://core.example")
        val now = System.currentTimeMillis()
        val record = OAuthCredentialBinding.encode(origin, "oauth-access-token", now + 60_000, "client-id", origin.toString())
        val status = OAuthCredentialBinding.status(record, origin, now + 120_000)
        assertTrue(status.configured)
        assertFalse(status.valid)
        assertEquals("", OAuthCredentialBinding.access(record, origin, now + 120_000))
    }

    @Test
    fun malformedAndLegacyValuesAreNotInterpretedAsTokens() {
        val origin = URI("https://core.example")
        listOf("", "legacy-token", "{}", "{\"schema_version\":1,\"origin\":\"https://other.example:443\"}").forEach { value ->
            assertEquals("", OAuthCredentialBinding.access(value, origin, 0))
            assertFalse(OAuthCredentialBinding.status(value, origin, 0).configured)
        }
    }
}
