package dev.agentdock.workbench.ui

import dev.agentdock.workbench.model.WorkbenchItem
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test

class WorkbenchSelectionPolicyTest {
    private val visibleTasks = listOf(WorkbenchItem("tsk_visible", "Visible task"))
    private val visibleConversations = listOf(WorkbenchItem("conv_visible", "Visible conversation"))

    @Test fun taskRefreshDropsDeletedOrFilteredIds() {
        assertEquals(
            setOf("tsk_visible"),
            visibleCheckedIds(visibleTasks, setOf("tsk_visible", "tsk_deleted"))
        )
    }

    @Test fun conversationRefreshKeepsOnlyVisibleConversationIds() {
        assertEquals(
            setOf("conv_visible"),
            visibleCheckedIds(visibleConversations, setOf("conv_visible", "conv_archived"))
        )
    }

    @Test fun emptyPageClearsBatchSelection() {
        assertTrue(visibleCheckedIds(emptyList(), setOf("tsk_deleted")).isEmpty())
    }
}
