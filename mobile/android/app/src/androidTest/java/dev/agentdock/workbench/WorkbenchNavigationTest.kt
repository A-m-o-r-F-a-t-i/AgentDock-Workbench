package dev.agentdock.workbench

import android.content.Intent
import android.content.res.Configuration
import androidx.compose.ui.test.*
import androidx.compose.ui.test.junit4.createEmptyComposeRule
import androidx.lifecycle.ViewModelProvider
import androidx.test.core.app.ActivityScenario
import androidx.test.ext.junit.runners.AndroidJUnit4
import androidx.test.platform.app.InstrumentationRegistry
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
    }

    @After fun close() {
        scenario.close()
        resetDisplayConfiguration()
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
        waitForText("Android Demo")
        compose.onNodeWithText("Android Demo").performScrollTo().performClick()
        compose.waitUntil(10000) { model.state.value.projectFiles.any { it.relativePath == "README.md" } }
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
        recreateAndBind()
        var fontScale = 0f
        scenario.onActivity { fontScale = it.resources.configuration.fontScale }
        assertTrue("font scale=$fontScale", fontScale >= 1.25f)
        navigate(WorkbenchScreen.Tasks)
        capture(directory, WorkbenchScreen.Tasks, "tasks-large-text")

        shell("settings put system accelerometer_rotation 0")
        shell("settings put system user_rotation 1")
        recreateAndBind()
        var orientation = Configuration.ORIENTATION_UNDEFINED
        scenario.onActivity { orientation = it.resources.configuration.orientation }
        assertEquals(Configuration.ORIENTATION_LANDSCAPE, orientation)
        navigate(WorkbenchScreen.Permissions)
        capture(directory, WorkbenchScreen.Permissions, "permissions-landscape")

        shell("settings put system user_rotation 0")
        shell("wm size 1280x800")
        shell("wm density 160")
        recreateAndBind()
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
        compose.waitUntil(10000) { compose.onAllNodesWithTag(tag).fetchSemanticsNodes().size == 1 }
    }

    private fun waitForText(text: String) {
        compose.waitUntil(10000) { compose.onAllNodesWithText(text).fetchSemanticsNodes().size == 1 }
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

    private fun recreateAndBind() {
        scenario.recreate()
        scenario.onActivity { model = ViewModelProvider(it)[WorkbenchViewModel::class.java] }
        compose.waitUntil(10000) { !model.state.value.loading && model.state.value.snapshot.fixture }
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
