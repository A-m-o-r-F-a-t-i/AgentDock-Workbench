package dev.agentdock.workbench.data

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertThrows
import org.junit.Assert.assertTrue
import org.junit.Test

class WorkbenchCommandsTest {
    @Test
    fun taskCreationMayBeGlobalOrWorkspaceBound() {
        val global = WorkbenchCommands.createTask("Android", "完成客户端", listOf("Actions 通过"), "").body()
        assertEquals("create", global.getString("action"))
        assertFalse(global.has("workspace_id"))
        val scoped = WorkbenchCommands.createTask("Android", "完成客户端", listOf("Actions 通过"), "wsp_android").body()
        assertEquals("wsp_android", scoped.getString("workspace_id"))
    }

    @Test
    fun taskLifecycleAndReviewFreezeExplicitTask() {
        val blocked = WorkbenchCommands.taskLifecycle("block", "tsk_android", "等待共享接口").body()
        assertEquals("block", blocked.getString("action"))
        assertEquals("tsk_android", blocked.getString("task_id"))
        val review = WorkbenchCommands.taskReview("tsk_android", "pass", "候选通过", listOf("API 26 通过"), emptyList()).body()
        assertEquals("final_review", review.getString("action"))
        assertEquals("pass", review.getString("status"))
        assertEquals(1, review.getJSONArray("verified").length())
        assertThrows(IllegalArgumentException::class.java) {
            WorkbenchCommands.taskReview("tsk_android", "pass", "缺少证据", emptyList(), emptyList())
        }
    }

    @Test
    fun threadCheckpointIsBoundAndBounded() {
        val value = WorkbenchCommands.taskThread(
            action = "thread_checkpoint",
            taskId = "tsk_android",
            threadId = "main",
            summary = "完成页面",
            nextAction = "运行 Actions",
            currentStepId = "S4",
            completedStepIds = listOf("S3")
        ).body()
        assertEquals("thread_checkpoint", value.getString("action"))
        assertEquals("main", value.getString("thread_id"))
        assertEquals("S4", value.getString("current_step_id"))
        assertEquals("S3", value.getJSONArray("completed_step_ids").getString(0))
        assertThrows(IllegalArgumentException::class.java) {
            WorkbenchCommands.taskThread("thread_switch", "tsk_android")
        }
    }

    @Test
    fun conversationBindingCarriesReadRevision() {
        val value = WorkbenchCommands.currentTask("conv_android", "tsk_android", "main", 7).body()
        assertEquals("tsk_android", value.getString("task_id"))
        assertEquals("main", value.getString("task_thread_id"))
        assertEquals(7L, value.getLong("binding_revision"))
        val detached = WorkbenchCommands.currentTask("conv_android", "", "", 8).body()
        assertTrue(detached.has("task_id"))
        assertEquals("", detached.getString("task_id"))
    }

    @Test
    fun permissionUpdateSeparatesScopeInheritanceAndFullConfirmation() {
        val workspace = WorkbenchCommands.permission(PermissionDraft(
            scope = "workspace", scopeId = "wsp_android", revision = 9, mode = "rules",
            confirmFull = false, customEnabled = false, inheritSettings = true,
            filesystem = "write", network = "allow", boundary = "none",
            approvalMode = "on-request", reviewer = "user",
            granularFileWrites = true, granularCommands = true, granularNetwork = true,
            granularMcp = true, granularManagement = true, granularOther = false
        )).body()
        assertEquals("workspace", workspace.getString("scope"))
        assertEquals("wsp_android", workspace.getString("scope_id"))
        assertTrue(workspace.getBoolean("inherit_settings"))
        assertFalse(workspace.has("settings"))
        assertThrows(IllegalArgumentException::class.java) {
            WorkbenchCommands.permission(PermissionDraft(
                "global", "", 9, "full", false, true, false,
                "write", "allow", "none", "on-request", "user",
                true, true, true, true, true, false
            ))
        }
        val conversation = WorkbenchCommands.permission(PermissionDraft(
            "conversation", "conv_android", 10, "readonly", false, false, false,
            "write", "allow", "none", "on-request", "user",
            true, true, true, true, true, false
        )).body()
        assertFalse(conversation.has("settings"))
        assertEquals("readonly", conversation.getString("mode"))
    }
}
