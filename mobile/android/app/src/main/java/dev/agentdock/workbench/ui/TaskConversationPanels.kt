package dev.agentdock.workbench.ui

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.material3.Button
import androidx.compose.material3.FilterChip
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.unit.dp
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import dev.agentdock.workbench.data.CorePages
import dev.agentdock.workbench.data.ManagementContract
import dev.agentdock.workbench.data.WorkbenchCommands
import dev.agentdock.workbench.model.WorkbenchItem
import org.json.JSONObject

@Composable
fun TaskCreationPanel(model: WorkbenchViewModel, workspaceId: String) {
    val resources by model.resources.state.collectAsStateWithLifecycle()
    val prompt = rememberCommandPrompt(model)
    var title by rememberSaveable { mutableStateOf("") }
    var goal by rememberSaveable { mutableStateOf("") }
    var conditions by rememberSaveable { mutableStateOf("") }
    var workspace by rememberSaveable(workspaceId) { mutableStateOf(workspaceId) }
    ResourceSection("创建持久任务") {
        ResourceField("标题", title, { title = it.take(512) })
        ResourceField("目标", goal, { goal = it.take(4096) }, multiline = true)
        ResourceField("完成条件（每行一条）", conditions, { conditions = it.take(16_384) }, multiline = true)
        ResourceField("工作区 ID", workspace, { workspace = it.take(128) })
        Button(
            onClick = {
                val frozen = conditions.lines().map(String::trim).filter(String::isNotEmpty).distinct()
                prompt.ask({ WorkbenchCommands.createTask(title, goal, frozen, workspace) })
            },
            enabled = !resources.actionBusy && title.isNotBlank() && goal.isNotBlank() && conditions.isNotBlank()
        ) { Text("创建任务") }
        Text("创建只保存任务计划，不自动运行工具。")
    }
}

@Composable
fun TaskLifecycleAndThreadsPanel(task: WorkbenchItem, detail: JSONObject?, model: WorkbenchViewModel) {
    val resources by model.resources.state.collectAsStateWithLifecycle()
    val prompt = rememberCommandPrompt(model)
    val taskId = task.id
    var summary by rememberSaveable(taskId) { mutableStateOf("") }
    var reviewStatus by rememberSaveable(taskId) { mutableStateOf("pass") }
    var verified by rememberSaveable(taskId) { mutableStateOf("") }
    var risks by rememberSaveable(taskId) { mutableStateOf("") }
    var selectedThread by rememberSaveable(taskId) { mutableStateOf("") }
    var threadTitle by rememberSaveable(taskId) { mutableStateOf("") }
    var threadSummary by rememberSaveable(taskId) { mutableStateOf("") }
    var nextAction by rememberSaveable(taskId) { mutableStateOf("") }
    var currentStep by rememberSaveable(taskId) { mutableStateOf("") }
    var completedSteps by rememberSaveable(taskId) { mutableStateOf("") }

    fun reloadThreads() {
        model.resources.read("task-threads", "/internal/runtime/tasks/${ManagementContract.id(taskId)}/threads")
    }
    fun readThread(threadId: String) {
        selectedThread = threadId
        model.resources.read("task-thread-detail", "/internal/runtime/tasks/${ManagementContract.id(taskId)}/threads/${ManagementContract.id(threadId)}")
        model.resources.read("task-thread-activity", "/internal/runtime/tasks/${ManagementContract.id(taskId)}/threads/${ManagementContract.id(threadId)}/activity?limit=100&after=0")
    }
    LaunchedEffect(taskId) { reloadThreads() }
    val threadsView = resources.views["task-threads"]
    val threads = remember(threadsView?.data) {
        runCatching { threadsView?.data?.let { CorePages.rows(it, "threads") }.orEmpty() }
    }
    val threadDetail = resources.views["task-thread-detail"]?.data?.let { it.optJSONObject("thread") ?: it }

    Column(verticalArrangement = Arrangement.spacedBy(10.dp)) {
        ResourceSection("任务生命周期") {
        ResourceField("说明／原因", summary, { summary = it.take(4096) }, multiline = true)
        ResourceActions {
            OutlinedButton(onClick = { prompt.ask({ WorkbenchCommands.taskLifecycle("resume", taskId, summary) }) }, enabled = !resources.actionBusy) { Text("恢复") }
            OutlinedButton(onClick = { prompt.ask({ WorkbenchCommands.taskLifecycle("block", taskId, summary) }) }, enabled = !resources.actionBusy && summary.isNotBlank()) { Text("标记受阻") }
            OutlinedButton(onClick = { prompt.ask({ WorkbenchCommands.taskLifecycle("cancel", taskId, summary) }) }, enabled = !resources.actionBusy && summary.isNotBlank()) { Text("取消任务") }
        }
        Text("最终审查")
        ResourceActions {
            listOf("pass" to "通过", "failed" to "未通过").forEach { (value, label) ->
                FilterChip(reviewStatus == value, { reviewStatus = value }, label = { Text(label) })
            }
        }
        ResourceField("已核实事实（每行一条）", verified, { verified = it.take(16_384) }, multiline = true)
        ResourceField("剩余风险（每行一条）", risks, { risks = it.take(16_384) }, multiline = true)
        ResourceActions {
            Button(onClick = {
                prompt.ask({
                    WorkbenchCommands.taskReview(
                        taskId,
                        reviewStatus,
                        summary,
                        verified.lines().map(String::trim).filter(String::isNotEmpty),
                        risks.lines().map(String::trim).filter(String::isNotEmpty)
                    )
                })
            }, enabled = !resources.actionBusy && summary.isNotBlank()) { Text("提交最终审查") }
            OutlinedButton(onClick = { prompt.ask({ WorkbenchCommands.taskLifecycle("complete", taskId, summary) }) }, enabled = !resources.actionBusy) { Text("完成任务") }
        }
        detail?.optJSONObject("final_review")?.let { ResourceObject(it) }
        }

        ResourceSection("任务分支") {
        ResourceFeedback(threadsView, ::reloadThreads)
        threads.exceptionOrNull()?.let { Text(it.message.orEmpty()) }
        if (threadsView?.data != null && threads.getOrDefault(emptyList()).isEmpty()) Text("此任务尚无分支记录。")
        threads.getOrDefault(emptyList()).forEach { row ->
            val id = row.optString("thread_id", row.optString("id"))
            Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                Column(Modifier.weight(1f)) {
                    Text(row.optString("title", id), style = MaterialTheme.typography.titleSmall)
                    ResourceObject(row, listOf("thread_id", "status", "current_step_id", "summary", "next_action", "updated_at"))
                }
                TextButton(onClick = { readThread(id) }) { Text(if (selectedThread == id) "刷新" else "详情") }
            }
        }
        ResourceField("新分支标题", threadTitle, { threadTitle = it.take(512) })
        ResourceField("分支摘要", threadSummary, { threadSummary = it.take(4096) }, multiline = true)
        ResourceField("下一动作", nextAction, { nextAction = it.take(4096) }, multiline = true)
        Button(onClick = {
            prompt.ask({ WorkbenchCommands.taskThread("thread_create", taskId, title = threadTitle, summary = threadSummary, nextAction = nextAction) }) { reloadThreads() }
        }, enabled = !resources.actionBusy && threadTitle.isNotBlank()) { Text("创建分支") }

        if (selectedThread.isNotBlank()) {
            Text("已选分支：$selectedThread", style = MaterialTheme.typography.titleSmall)
            ResourceFeedback(resources.views["task-thread-detail"]) { readThread(selectedThread) }
            threadDetail?.let { ResourceObject(it) }
            ResourceField("当前步骤 ID", currentStep, { currentStep = it.take(128) })
            ResourceField("本次完成步骤 ID（逗号或换行分隔）", completedSteps, { completedSteps = it.take(4096) }, multiline = true)
            ResourceActions {
                OutlinedButton(onClick = {
                    prompt.ask({ WorkbenchCommands.taskThread("thread_switch", taskId, selectedThread) }) { reloadThreads() }
                }, enabled = !resources.actionBusy) { Text("切换为活动分支") }
                OutlinedButton(onClick = {
                    prompt.ask({ WorkbenchCommands.taskThread("thread_block", taskId, selectedThread, summary = threadSummary, nextAction = nextAction) }) { readThread(selectedThread) }
                }, enabled = !resources.actionBusy && threadSummary.isNotBlank()) { Text("阻塞") }
                OutlinedButton(onClick = {
                    prompt.ask({ WorkbenchCommands.taskThread("thread_resume", taskId, selectedThread, summary = threadSummary, nextAction = nextAction) }) { readThread(selectedThread) }
                }, enabled = !resources.actionBusy) { Text("恢复") }
                OutlinedButton(onClick = {
                    prompt.ask({ WorkbenchCommands.taskThread("thread_close", taskId, selectedThread, summary = threadSummary, nextAction = nextAction) }) { reloadThreads() }
                }, enabled = !resources.actionBusy) { Text("关闭") }
            }
            Button(onClick = {
                val completed = completedSteps.split(',', '，', '\n').map(String::trim).filter(String::isNotEmpty).distinct()
                prompt.ask({
                    WorkbenchCommands.taskThread(
                        "thread_checkpoint", taskId, selectedThread,
                        summary = threadSummary,
                        nextAction = nextAction,
                        currentStepId = currentStep,
                        completedStepIds = completed
                    )
                }) { readThread(selectedThread) }
            }, enabled = !resources.actionBusy && (threadSummary.isNotBlank() || nextAction.isNotBlank() || currentStep.isNotBlank() || completedSteps.isNotBlank())) { Text("保存分支检查点") }
            val activity = resources.views["task-thread-activity"]?.data
            activity?.optJSONArray("events")?.let { events ->
                Text("分支活动", style = MaterialTheme.typography.titleSmall)
                for (index in 0 until minOf(events.length(), 100)) {
                    events.optJSONObject(index)?.let { ResourceObject(it, listOf("event_id", "kind", "status", "title", "summary", "created_at")) }
                }
            }
        }
        }
    }
}

@Composable
fun ConversationBindingPanel(conversation: WorkbenchItem, detail: JSONObject?, model: WorkbenchViewModel) {
    val resources by model.resources.state.collectAsStateWithLifecycle()
    val prompt = rememberCommandPrompt(model)
    val record = detail?.optJSONObject("conversation") ?: detail
    val state = record?.optJSONObject("state")
    val revision = state?.optLong("binding_revision", -1) ?: -1
    var taskId by rememberSaveable(conversation.id) { mutableStateOf(state?.optString("active_task_id").orEmpty()) }
    var threadId by rememberSaveable(conversation.id) { mutableStateOf(state?.optString("active_task_thread_id").orEmpty()) }
    LaunchedEffect(revision) {
        if (revision >= 0) {
            taskId = state?.optString("active_task_id").orEmpty()
            threadId = state?.optString("active_task_thread_id").orEmpty()
        }
    }
    ResourceSection("任务关联与后续执行") {
        Text("当前 binding_revision：${if (revision >= 0) revision else "未读取"}")
        state?.let { ResourceObject(it, listOf("active_task_id", "active_task_thread_id", "workspace_id", "binding_revision", "updated_at")) }
        record?.optJSONArray("task_ids")?.let { ids ->
            Text("已关联任务：${(0 until ids.length()).joinToString("、") { ids.optString(it) }}")
        }
        ResourceField("任务 ID", taskId, { taskId = it.take(128) })
        ResourceField("分支 ID（留空使用活动分支）", threadId, { threadId = it.take(128) })
        ResourceActions {
            OutlinedButton(onClick = {
                prompt.ask({ WorkbenchCommands.link(conversation.id, taskId) }) { model.selectConversation(conversation) }
            }, enabled = !resources.actionBusy && taskId.isNotBlank()) { Text("仅建立关联") }
            Button(onClick = {
                prompt.ask({ WorkbenchCommands.currentTask(conversation.id, taskId, threadId, revision) }) { model.selectConversation(conversation) }
            }, enabled = !resources.actionBusy && taskId.isNotBlank() && revision >= 0) { Text("设为当前任务") }
            OutlinedButton(onClick = {
                prompt.ask({ WorkbenchCommands.currentTask(conversation.id, "", "", revision) }) { model.selectConversation(conversation) }
            }, enabled = !resources.actionBusy && revision >= 0) { Text("解除当前绑定") }
        }
        ResourceActions {
            OutlinedButton(onClick = { prompt.ask({ WorkbenchCommands.lifecycle(conversation.id, "resume") }) { model.selectConversation(conversation) } }, enabled = !resources.actionBusy) { Text("恢复对话") }
            OutlinedButton(onClick = { prompt.ask({ WorkbenchCommands.lifecycle(conversation.id, "terminate") }) { model.selectConversation(conversation) } }, enabled = !resources.actionBusy) { Text("终止对话") }
        }
        Text("绑定只影响后续调用；已经运行或等待审批的调用保留原任务、分支和工作区快照。")
    }
}
