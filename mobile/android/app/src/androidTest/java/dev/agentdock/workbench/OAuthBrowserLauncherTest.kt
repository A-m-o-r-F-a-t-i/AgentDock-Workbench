package dev.agentdock.workbench

import android.content.ActivityNotFoundException
import android.content.ContextWrapper
import android.content.Intent
import android.content.pm.PackageManager
import androidx.test.ext.junit.runners.AndroidJUnit4
import dev.agentdock.workbench.data.OAuthBrowserLauncher
import java.net.URI
import org.junit.Assert.*
import org.junit.Test
import org.junit.runner.RunWith

@RunWith(AndroidJUnit4::class)
class OAuthBrowserLauncherTest {
    @Test fun browserIsLaunchedWithoutQueryingPackageVisibility() {
        var actual: Intent? = null
        val context = object : ContextWrapper(null) {
            override fun getPackageManager(): PackageManager = error("Pre-query is forbidden in this test")
            override fun startActivity(intent: Intent) { actual = intent }
        }
        OAuthBrowserLauncher.open(context, URI("https://core.example/oauth/authorize?state=fixture"))
        assertEquals(Intent.ACTION_VIEW, actual?.action)
        assertEquals("https://core.example/oauth/authorize?state=fixture", actual?.dataString)
        assertTrue(checkNotNull(actual).flags and Intent.FLAG_ACTIVITY_NEW_TASK != 0)
    }

    @Test fun missingBrowserIsReportedAfterActualLaunchAttempt() {
        var attempted = false
        val context = object : ContextWrapper(null) {
            override fun startActivity(intent: Intent) { attempted = true; throw ActivityNotFoundException() }
        }
        val error = assertThrows(IllegalStateException::class.java) {
            OAuthBrowserLauncher.open(context, URI("https://core.example/oauth/authorize"))
        }
        assertTrue(attempted); assertTrue(error.cause is ActivityNotFoundException)
    }
}
