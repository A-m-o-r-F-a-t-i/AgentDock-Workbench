package dev.agentdock.releasecheck;

import android.accessibilityservice.AccessibilityServiceInfo;
import android.app.UiAutomation;
import android.content.Context;
import android.content.pm.ApplicationInfo;
import android.os.Build;
import android.os.SystemClock;
import android.util.Log;
import android.view.accessibility.AccessibilityNodeInfo;
import androidx.test.ext.junit.runners.AndroidJUnit4;
import androidx.test.platform.app.InstrumentationRegistry;
import androidx.test.uiautomator.UiDevice;
import dalvik.system.PathClassLoader;
import org.junit.Test;
import org.junit.runner.RunWith;
import static org.junit.Assert.*;

/** No app implementation dependency: the installed, minified release is the target. */
@RunWith(AndroidJUnit4.class)
public class ReleaseStartupTest {
    private static final String TARGET = "dev.agentdock.workbench";
    private static final String LOG_TAG = "ReleaseStartupCheck";
    private static final long WINDOW_TIMEOUT_MS = 10_000L;
    private static final long WINDOW_POLL_MS = 100L;

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
        UiAutomation automation = InstrumentationRegistry.getInstrumentation().getUiAutomation();
        AccessibilityServiceInfo original = automation.getServiceInfo();
        AccessibilityServiceInfo observation = automation.getServiceInfo();
        observation.flags |= AccessibilityServiceInfo.FLAG_RETRIEVE_INTERACTIVE_WINDOWS;
        automation.setServiceInfo(observation);
        int attempt = 0;
        device.wakeUp();
        device.executeShellCommand("logcat -c");
        try {
            for (; attempt < 10; attempt++) {
                prepareForegroundHolder(device, automation, attempt == 0);
                device.executeShellCommand("am force-stop " + TARGET);
                String launch = device.executeShellCommand("am start -W -n " + TARGET + "/.MainActivity");
                assertTrue("Cold start " + attempt + " failed: " + launch, launch.contains("Status: ok"));
                String pid = device.executeShellCommand("pidof " + TARGET).trim();
                assertFalse("Release exited after cold start " + attempt, pid.isEmpty());
                assertTrue("Release window was not observable at cold start " + attempt,
                    awaitFreshWindow(automation, TARGET));
                SystemClock.sleep(1_500L);
                assertEquals("Release restarted after cold start " + attempt, pid,
                    device.executeShellCommand("pidof " + TARGET).trim());
            }
            String crashes = device.executeShellCommand("logcat -d -s AndroidRuntime:E");
            assertFalse("Release crash detected: " + crashes, crashes.contains("Process: " + TARGET + ","));
        } catch (AssertionError | Exception failure) {
            captureFailure(device, attempt);
            throw failure;
        } finally {
            try { device.executeShellCommand("am force-stop " + TARGET); }
            finally { automation.setServiceInfo(original); }
        }
    }

    private static void prepareForegroundHolder(UiDevice device, UiAutomation automation, boolean first) throws Exception {
        String testPackage = InstrumentationRegistry.getInstrumentation().getContext().getPackageName();
        String holder = testPackage + "/" + ColdStartHostActivity.class.getName();
        String launch = device.executeShellCommand("am start -W -n " + holder);
        assertTrue("Test foreground holder failed: " + launch, launch.contains("Status: ok"));
        // Google API emulator Launcher can ANR after the preceding display-size
        // tests. Reset only that disposable emulator component, never the target,
        // before any measured launch. The holder prevents returning to it later.
        if (first && "1".equals(device.executeShellCommand("getprop ro.kernel.qemu").trim())) {
            Log.w(LOG_TAG, "Preparing emulator: stopping Pixel Launcher before measured cold starts");
            device.executeShellCommand("am force-stop com.google.android.apps.nexuslauncher");
        }
        assertTrue("Test foreground holder is not visible; no target launch was retried",
            awaitFreshWindow(automation, testPackage));
    }

    private static boolean awaitFreshWindow(UiAutomation automation, String packageName) {
        long deadline = SystemClock.uptimeMillis() + WINDOW_TIMEOUT_MS;
        do {
            // Android 14 can cache the old active root while window registration
            // times out. Refresh observations; never relaunch a failed start.
            if (Build.VERSION.SDK_INT >= 34) automation.clearCache();
            AccessibilityNodeInfo root = automation.getRootInActiveWindow();
            if (root != null) {
                try {
                    if (packageName.contentEquals(root.getPackageName() == null ? "" : root.getPackageName())
                            && root.isVisibleToUser()) return true;
                } finally {
                    root.recycle();
                }
            }
            SystemClock.sleep(WINDOW_POLL_MS);
        } while (SystemClock.uptimeMillis() < deadline);
        return false;
    }

    private static void captureFailure(UiDevice device, int attempt) {
        // Preserve the failed window before finally stops the target. Collection
        // failures must never replace the original assertion or turn it green.
        try {
            String root = "/sdcard/Download/agentdock-release-startup";
            device.executeShellCommand("mkdir -p " + root);
            device.executeShellCommand("screencap -p " + root + "/cold-" + attempt + ".png");
            for (String command : new String[]{"pidof " + TARGET, "dumpsys window windows", "dumpsys activity activities"}) {
                Log.e(LOG_TAG, "Cold start " + attempt + ": " + command);
                String[] lines = device.executeShellCommand(command).split("\n");
                for (int index = 0; index < Math.min(lines.length, 200); index++)
                    Log.e(LOG_TAG, lines[index]);
            }
        } catch (Exception collectionFailure) {
            Log.e(LOG_TAG, "Could not collect the failed observation", collectionFailure);
        }
    }
}
