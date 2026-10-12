package dev.agentdock.releasecheck;

import android.content.Context;
import android.content.pm.ApplicationInfo;
import android.os.SystemClock;
import androidx.test.ext.junit.runners.AndroidJUnit4;
import androidx.test.platform.app.InstrumentationRegistry;
import androidx.test.uiautomator.By;
import androidx.test.uiautomator.UiDevice;
import androidx.test.uiautomator.Until;
import dalvik.system.PathClassLoader;
import org.junit.Test;
import org.junit.runner.RunWith;
import static org.junit.Assert.*;

/** No app implementation dependency: the installed, minified release is the target. */
@RunWith(AndroidJUnit4.class)
public class ReleaseStartupTest {
    private static final String TARGET = "dev.agentdock.workbench";

    @Test public void generatedWorkDatabaseRetainsItsReflectiveConstructor() throws Exception {
        Context context = InstrumentationRegistry.getInstrumentation().getContext();
        ApplicationInfo app = context.getPackageManager().getApplicationInfo(TARGET, 0);
        assertEquals("Target must be a non-debug release APK", 0, app.flags & ApplicationInfo.FLAG_DEBUGGABLE);
        // A boot-only parent prevents the test APK's own Room or AndroidX
        // dependencies from satisfying a missing class in the release APK.
        ClassLoader loader = new PathClassLoader(app.sourceDir, ClassLoader.getSystemClassLoader().getParent());
        Class<?> implementation = Class.forName("androidx.work.impl.WorkDatabase_Impl", true, loader);
        assertSame(loader, implementation.getClassLoader());
        assertNotNull(implementation.getConstructor().newInstance());
    }

    @Test public void packagedReleaseSurvivesTenColdStarts() throws Exception {
        UiDevice device = UiDevice.getInstance(InstrumentationRegistry.getInstrumentation());
        device.executeShellCommand("logcat -c");
        try {
            for (int attempt = 0; attempt < 10; attempt++) {
                device.executeShellCommand("am force-stop " + TARGET);
                String launch = device.executeShellCommand("am start -W -n " + TARGET + "/.MainActivity");
                assertTrue("Cold start " + attempt + " failed: " + launch, launch.contains("Status: ok"));
                assertTrue("Release UI was not rendered at cold start " + attempt,
                    device.wait(Until.hasObject(By.pkg(TARGET)), 10_000L));
                String pid = device.executeShellCommand("pidof " + TARGET).trim();
                assertFalse("Release exited after cold start " + attempt, pid.isEmpty());
                SystemClock.sleep(1_500L);
                assertEquals("Release restarted after cold start " + attempt, pid,
                    device.executeShellCommand("pidof " + TARGET).trim());
            }
            String crashes = device.executeShellCommand("logcat -d -s AndroidRuntime:E");
            assertFalse("Release crash detected: " + crashes, crashes.contains("Process: " + TARGET + ","));
        } finally {
            device.executeShellCommand("am force-stop " + TARGET);
        }
    }
}
