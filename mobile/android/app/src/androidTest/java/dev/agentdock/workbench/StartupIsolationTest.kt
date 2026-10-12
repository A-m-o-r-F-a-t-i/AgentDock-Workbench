package dev.agentdock.workbench

import android.content.ComponentName
import android.content.Context
import android.content.pm.PackageManager
import androidx.startup.AppInitializer
import androidx.test.core.app.ApplicationProvider
import androidx.test.ext.junit.runners.AndroidJUnit4
import androidx.work.Configuration
import androidx.work.WorkManager
import androidx.work.WorkManagerInitializer
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertTrue
import org.junit.Test
import org.junit.runner.RunWith

@RunWith(AndroidJUnit4::class)
class StartupIsolationTest {
    @Test
    fun providerStartupDoesNotEagerlyInitializeWorkManager() {
        val context = ApplicationProvider.getApplicationContext<Context>()
        val provider = context.packageManager.getProviderInfo(
            ComponentName(context, "androidx.startup.InitializationProvider"), PackageManager.GET_META_DATA
        )
        assertFalse(provider.metaData?.containsKey("androidx.work.WorkManagerInitializer") == true)
        assertFalse(AppInitializer.getInstance(context).isEagerlyInitialized(WorkManagerInitializer::class.java))
        assertTrue(context.applicationContext is Configuration.Provider)
        assertNotNull(WorkManager.getInstance(context))
    }
}
