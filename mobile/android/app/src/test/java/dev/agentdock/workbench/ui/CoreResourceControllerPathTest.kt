package dev.agentdock.workbench.ui

import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

class CoreResourceControllerPathTest {
    @Test
    fun permitsRuntimeAndExactOAuthDiscoveryRoutesOnly() {
        assertTrue(CoreResourceController.allowedReadPath("/internal/runtime/status"))
        assertTrue(CoreResourceController.allowedReadPath("/.well-known/oauth-authorization-server"))
        assertTrue(CoreResourceController.allowedReadPath("/.well-known/oauth-protected-resource/mcp"))
        assertFalse(CoreResourceController.allowedReadPath("/.well-known/oauth-authorization-server?next=1"))
        assertFalse(CoreResourceController.allowedReadPath("/oauth/token"))
        assertFalse(CoreResourceController.allowedReadPath("//internal/runtime/status"))
    }
}
