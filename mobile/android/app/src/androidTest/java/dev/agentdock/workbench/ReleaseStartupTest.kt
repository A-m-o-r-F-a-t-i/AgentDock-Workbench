package dev.agentdock.workbench

import android.content.Context
import androidx.test.core.app.ActivityScenario
import androidx.test.core.app.ApplicationProvider
import androidx.test.ext.junit.runners.AndroidJUnit4
import androidx.work.WorkManager
import org.junit.Assert.assertNotNull
import org.junit.Test
import org.junit.runner.RunWith

/** Run against the minified release target as well as the regular debug target. */
@RunWith(AndroidJUnit4::class)
class ReleaseStartupTest {
    @Test
    fun generatedWorkDatabaseRetainsItsReflectiveConstructor() {
        val implementation = Class.forName("androidx.work.impl.WorkDatabase_Impl")
        assertNotNull(implementation.getDeclaredConstructor().newInstance())
    }

    @Test
    fun activityAndWorkManagerStartInThePackagedApplication() {
        val context = ApplicationProvider.getApplicationContext<Context>()
        assertNotNull(WorkManager.getInstance(context))
        ActivityScenario.launch(MainActivity::class.java).use { scenario ->
            scenario.onActivity { activity -> assertNotNull(activity.application) }
        }
    }
}
