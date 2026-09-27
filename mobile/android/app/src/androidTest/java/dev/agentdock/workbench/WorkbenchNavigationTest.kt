package dev.agentdock.workbench

import android.content.Intent
import android.content.res.Configuration
import android.view.View
import android.view.ViewGroup
import androidx.compose.ui.platform.AbstractComposeView
import androidx.compose.ui.test.*
import androidx.compose.ui.test.junit4.createEmptyComposeRule
import androidx.lifecycle.ViewModelProvider
import androidx.test.core.app.ActivityScenario
import androidx.test.ext.junit.runners.AndroidJUnit4
import androidx.test.platform.app.InstrumentationRegistry
import androidx.test.runner.lifecycle.ActivityLifecycleMonitorRegistry
import androidx.test.runner.lifecycle.Stage
import dev.agentdock.workbench.model.WorkbenchScreen
import dev.agentdock.workbench.ui.WorkbenchViewModel
import org.junit.After
import org.junit.Assert.*
import org.junit.Before
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import android.os.ParcelFileDescriptor

@RunWith(AndroidJUnit4::class)
class WorkbenchNavigationTest {
    @get:Rule val compose = createEmptyComposeRule()
    private lateinit var scenario: ActivityScenario<MainActivity>
    private lateinit var model: WorkbenchViewModel

    @Before fun launchFixture() {
        resetDisplayConfiguration()
        val context = InstrumentationRegistry.getInstrumentation().targetContext
        scenario = ActivityScenario.launch(Intent(context, MainActivity::class.java)
            .addFlags(Intent.FLAG_ACTIVITY_NEW_TASK).putExtra(MainActivity.EXTRA_FIXTURE, true))
        scenario.onActivity { model = ViewModelProvider(it)[WorkbenchViewModel::class.java] }
        compose.waitUntil(10000) { !model.state.value.loading && model.state.value.snapshot.fixture }
        awaitConfiguration { true }
    }

    @After fun close() {
        try {
            if (::scenario.isInitialized) scenario.close()
        } finally {
            resetDisplayConfiguration()
        }
    }

    @Test fun everyWorkbenchPageIsReachableOnCompactLayout() {
        WorkbenchScreen.entries.forEach(::navigate)
    }

    @Test fun captureKeyPages() {
        // Instrumentation executes with the target UID, not the test APK's UID.
        // Shell-owned CI evidence survives orchestrator app-data clearing.
        val directory = "/sdcard/Download/agentdock-wb07-screenshots"
        // UiAutomation uses Runtime.exec: no shell operators or quote parsing.
        shell("mkdir -p $directory")
        assertEquals(directory, shell("ls -d $directory").trim())
        WorkbenchScreen.entries.forEach { screen ->
            navigate(screen)
            capture(directory, screen, screen.route)
        }
        navigate(WorkbenchScreen.Tasks)
        compose.runOnIdle { model.setFixtureScenario("empty") }
        compose.waitUntil(10000) { !model.state.value.loading && model.state.value.snapshot.tasks.isEmpty() }
        compose.onNodeWithTag("screen-tasks").performScrollToIndex(2)
        waitForNodeWithTag("tasks-empty")
        compose.onNodeWithTag("tasks-empty").performScrollTo().assertIsDisplayed()
        capture(directory, WorkbenchScreen.Tasks, "tasks-empty")
        compose.runOnIdle { model.setFixtureScenario("error") }
        compose.waitUntil(10000) { !model.state.value.loading && model.state.value.snapshot.errors.containsKey("tasks") }
        waitForNodeWithTag("resource-error")
        compose.onNodeWithTag("resource-error").assertIsDisplayed()
        compose.onNodeWithTag("tasks-empty").assertDoesNotExist()
        capture(directory, WorkbenchScreen.Tasks, "tasks-error")
    }

    @Test fun restoresNavigationAndCommittedFiltersAfterRecreation() {
        navigate(WorkbenchScreen.Tasks)
        compose.onNodeWithTag("tasks-search").performTextInput("回执检查")
        compose.onNodeWithText("应用筛选").performScrollTo().performClick()
        compose.waitUntil(10000) { !model.state.value.loading }
        scenario.recreate()
        awaitConfiguration { true }
        scenario.onActivity { model = ViewModelProvider(it)[WorkbenchViewModel::class.java] }
        compose.onNodeWithTag("screen-tasks").assertIsDisplayed()
        compose.onNodeWithTag("tasks-search").assertTextContains("回执检查")
        navigate(WorkbenchScreen.Settings)
        // Exercise the Activity's real Compose BackHandler dispatch without
        // Espresso's focus-sensitive root selection after recreation.
        scenario.onActivity { it.onBackPressedDispatcher.onBackPressed() }
        compose.waitUntil(10000) { model.state.value.screen == WorkbenchScreen.Tasks }
        compose.onNodeWithTag("screen-tasks").assertIsDisplayed()
    }

    @Test fun fixtureNeverDispatchesRealTermuxOrCoreWrites() {
        compose.runOnIdle {
            val before = model.state.value.settings.desiredNodeState
            model.runTermux("start")
            assertEquals(before, model.state.value.settings.desiredNodeState)
            assertFalse(model.state.value.actionBusy)
            assertTrue(model.state.value.message.contains("禁止实际写入"))
            model.manage("tasks", listOf("tsk_android"), "delete", confirmed = true)
            assertNull(model.state.value.batchResult)
            val endpoint = model.state.value.settings.endpoint
            model.saveConnection("https://fixture.invalid", true, "fixture-do-not-store")
            assertEquals(endpoint, model.state.value.settings.endpoint)
            assertTrue(model.state.value.message.contains("禁止实际写入"))
            model.clearBearer()
            assertTrue(model.state.value.message.contains("禁止实际写入"))
        }
    }

    @Test fun draftSurvivesNavigationWithoutSending() {
        navigate(WorkbenchScreen.Conversations)
        compose.runOnIdle { model.selectConversation(model.state.value.snapshot.conversations.first()) }
        navigate(WorkbenchScreen.InsertAndStop)
        compose.onNodeWithTag("insertion-text").performTextInput("保留这条补充要求")
        navigate(WorkbenchScreen.Home)
        navigate(WorkbenchScreen.InsertAndStop)
        compose.onNodeWithTag("insertion-text").assertTextContains("保留这条补充要求")
        scenario.recreate()
        awaitConfiguration { true }
        compose.onNodeWithTag("insertion-text").assertTextContains("保留这条补充要求")
    }

    @Test fun fixtureOAuthDiscoveryUsesCoreRoutesWithoutDeviceWrites() {
        navigate(WorkbenchScreen.CoreConnections)
        compose.waitUntil(10000) {
            val metadata = model.resources.state.value.views["oauth-metadata"]?.data
            metadata?.optString("authorization_endpoint") == "http://127.0.0.1:8765/oauth/authorize" &&
                metadata.optString("token_endpoint") == "http://127.0.0.1:8765/oauth/token"
        }
        assertFalse(model.resources.state.value.views["oauth-metadata"]?.error.orEmpty().isNotBlank())
        assertFalse(model.state.value.credentialAvailable)
    }

    @Test fun fixtureProjectTreeIsInspectableWithoutStorageWrites() {
        navigate(WorkbenchScreen.ProjectsFiles)
        compose.waitUntil(10000) { model.state.value.projectRoots.isNotEmpty() }
        compose.onNodeWithTag("screen-projects").performScrollToIndex(3)
        waitForText("Android Demo")
        compose.onNodeWithText("Android Demo").performScrollTo().performClick()
        compose.waitUntil(10000) { model.state.value.projectFiles.any { it.relativePath == "README.md" } }
        compose.onNodeWithTag("screen-projects").performScrollToIndex(4)
        waitForText("README.md · 128 B")
        compose.onNodeWithText("README.md · 128 B").performScrollTo().performClick()
        compose.waitUntil(10000) { model.state.value.projectPreview.contains("No real file was read") }
    }

    @Test fun adaptiveConfigurationsRemainNavigableAndProduceEvidence() {
        val directory = "/sdcard/Download/agentdock-wb07-screenshots"
        shell("mkdir -p $directory")

        model.updateSettings { it.copy(theme = "dark") }
        compose.waitUntil(10000) { model.state.value.settings.theme == "dark" }
        navigate(WorkbenchScreen.Home)
        capture(directory, WorkbenchScreen.Home, "home-dark")

        model.updateSettings { it.copy(theme = "light") }
        compose.waitUntil(10000) { model.state.value.settings.theme == "light" }
        shell("settings put system font_scale 1.30")
        awaitConfiguration { it.fontScale >= 1.25f }
        var fontScale = 0f
        scenario.onActivity { fontScale = it.resources.configuration.fontScale }
        assertTrue("font scale=$fontScale", fontScale >= 1.25f)
        navigate(WorkbenchScreen.Tasks)
        capture(directory, WorkbenchScreen.Tasks, "tasks-large-text")

        shell("settings put system accelerometer_rotation 0")
        shell("settings put system user_rotation 1")
        awaitConfiguration { it.orientation == Configuration.ORIENTATION_LANDSCAPE }
        var orientation = Configuration.ORIENTATION_UNDEFINED
        scenario.onActivity { orientation = it.resources.configuration.orientation }
        assertEquals(Configuration.ORIENTATION_LANDSCAPE, orientation)
        navigate(WorkbenchScreen.Permissions)
        capture(directory, WorkbenchScreen.Permissions, "permissions-landscape")

        shell("settings put system user_rotation 0")
        awaitConfiguration { it.orientation == Configuration.ORIENTATION_PORTRAIT }
        shell("wm size 1280x800")
        awaitConfiguration { it.orientation == Configuration.ORIENTATION_LANDSCAPE }
        shell("wm density 160")
        awaitConfiguration { it.smallestScreenWidthDp >= 600 && it.densityDpi == 160 }
        var smallestWidth = 0
        scenario.onActivity { smallestWidth = it.resources.configuration.smallestScreenWidthDp }
        assertTrue("smallestScreenWidthDp=$smallestWidth", smallestWidth >= 600)
        navigate(WorkbenchScreen.ProjectsFiles)
        compose.waitUntil(10000) { model.state.value.projectRoots.isNotEmpty() }
        capture(directory, WorkbenchScreen.ProjectsFiles, "projects-expanded")

        model.updateSettings { it.copy(density = "compact") }
        compose.waitUntil(10000) { model.state.value.settings.density == "compact" }
        navigate(WorkbenchScreen.Settings)
        capture(directory, WorkbenchScreen.Settings, "settings-expanded-compact")
    }

    private fun waitForNodeWithTag(tag: String) {
        compose.waitUntil(10000) { compose.onAllNodesWithTag(tag).fetchSemanticsNodes().isNotEmpty() }
    }

    private fun waitForText(text: String) {
        compose.waitUntil(10000) { compose.onAllNodesWithText(text).fetchSemanticsNodes().isNotEmpty() }
    }

    private fun navigate(screen: WorkbenchScreen) {
        compose.onNodeWithTag("open-navigation").performClick()
        compose.onNodeWithTag("nav-${screen.route}").performScrollTo().performClick()
        compose.onNodeWithTag("screen-${screen.route}").assertIsDisplayed()
    }

    private fun capture(directory: String, screen: WorkbenchScreen, name: String) {
        require(Regex("[a-z-]+").matches(name))
        compose.waitForIdle()
        compose.onNodeWithTag("screen-${screen.route}").assertIsDisplayed()
        val path = "$directory/$name.png"
        shell("rm -f $path")
        shell("screencap -p $path")
        val length = shell("stat -c %s $path").trim().toLongOrNull()
        check(length != null && length > 8) { "Screenshot was not saved: $name" }
    }

    private fun awaitConfiguration(matches: (Configuration) -> Boolean) {
        // Settings/WindowManager already trigger asynchronous system recreation.
        // Starting a second ActivityScenario.recreate here races the old Activity's
        // destruction (especially on API 26). Observe the real resumed replacement
        // and its applied configuration instead of replaying lifecycle mutations.
        val retainedModel = model
        val instrumentation = InstrumentationRegistry.getInstrumentation()
        compose.waitUntil(10000) {
            var candidate: MainActivity? = null
            instrumentation.runOnMainSync {
                val activity = ActivityLifecycleMonitorRegistry.getInstance()
                    .getActivitiesInStage(Stage.RESUMED)
                    .filterIsInstance<MainActivity>()
                    .singleOrNull()
                if (activity != null && !activity.isDestroyed && !activity.isFinishing &&
                    matches(activity.resources.configuration) &&
                    hasAttachedComposition(activity.window.decorView)) {
                    candidate = activity
                }
            }
            if (candidate == null) {
                false
            } else {
                // RESUMED can precede attachment of the replacement Compose root.
                // Absence is a readiness state here, not an assertion to replay.
                val hasNavigation = compose.onAllNodesWithTag("open-navigation")
                    .fetchSemanticsNodes(atLeastOneRootRequired = false).size == 1
                var stillCurrent = false
                instrumentation.runOnMainSync {
                    val current = ActivityLifecycleMonitorRegistry.getInstance()
                        .getActivitiesInStage(Stage.RESUMED)
                        .filterIsInstance<MainActivity>().singleOrNull()
                    if (current != null && current === candidate &&
                        !current.isDestroyed && !current.isFinishing &&
                        matches(current.resources.configuration) &&
                        hasAttachedComposition(current.window.decorView)) {
                        model = ViewModelProvider(current)[WorkbenchViewModel::class.java]
                        stillCurrent = true
                    }
                }
                hasNavigation && stillCurrent
            }
        }
        assertSame("Configuration recreation must retain the Workbench ViewModel", retainedModel, model)
        compose.waitUntil(10000) { !model.state.value.loading && model.state.value.snapshot.fixture }
        compose.waitForIdle()
    }

    private fun hasAttachedComposition(view: View): Boolean {
        if (!view.isAttachedToWindow) return false
        if (view is AbstractComposeView) {
            return view.hasComposition && view.width > 0 && view.height > 0
        }
        return view is ViewGroup && (0 until view.childCount).any {
            hasAttachedComposition(view.getChildAt(it))
        }
    }

    private fun resetDisplayConfiguration() {
        shell("settings put system font_scale 1.0")
        shell("settings put system accelerometer_rotation 0")
        shell("settings put system user_rotation 0")
        shell("wm size reset")
        shell("wm density reset")
    }

    private fun shell(command: String): String {
        val descriptor = InstrumentationRegistry.getInstrumentation().uiAutomation.executeShellCommand(command)
        return ParcelFileDescriptor.AutoCloseInputStream(descriptor).bufferedReader().use { it.readText() }
    }
}
