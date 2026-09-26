package dev.agentdock.workbench.ui

import androidx.compose.foundation.clickable
import androidx.compose.foundation.horizontalScroll
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items as lazyItems
import androidx.compose.material3.Button
import androidx.compose.material3.Card
import androidx.compose.material3.ElevatedCard
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.ListItem
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.remember
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import dev.agentdock.workbench.model.BridgeOperation
import dev.agentdock.workbench.model.WorkbenchItem
import dev.agentdock.workbench.model.WorkbenchScreen
import java.nio.charset.StandardCharsets

@Composable
fun WorkbenchPage(
    state: WorkbenchUiState,
    viewModel: WorkbenchViewModel,
    modifier: Modifier = Modifier,
    contentPadding: PaddingValues = PaddingValues()
) {
    val pageModifier = modifier.padding(contentPadding).padding(horizontal = 16.dp, vertical = if (state.settings.density == "compact") 6.dp else 12.dp)
    val errorKeys = when (state.screen) {
        WorkbenchScreen.CallDetail -> listOf("calls")
        WorkbenchScreen.Plugins -> listOf("plugins", "mcp")
        WorkbenchScreen.InsertAndStop -> listOf("conversations", "insert")
        else -> listOf(state.screen.route)
    }
    val errors = errorKeys.mapNotNull { state.snapshot.errors[it] }
    if (state.screen != WorkbenchScreen.Home && errors.isNotEmpty()) {
        Column(pageModifier.verticalScroll(rememberScrollState()), verticalArrangement = Arrangement.spacedBy(12.dp)) {
            Text("Core 数据读取失败", modifier = Modifier.testTag("resource-error"), style = MaterialTheme.typography.titleLarge)
            errors.forEach { Text(it) }
            Button(onClick = viewModel::refresh) { Text("重新读取") }
        }
        return
    }
    when (state.screen) {
        WorkbenchScreen.Home -> HomePage(state, viewModel, pageModifier)
        WorkbenchScreen.Workspaces -> WorkspacePage(state, viewModel, pageModifier)
        WorkbenchScreen.Conversations -> ManagementPage("conversations", state, viewModel, pageModifier)
        WorkbenchScreen.Tasks -> ManagementPage("tasks", state, viewModel, pageModifier)
        WorkbenchScreen.Activity -> ActivityPage(state, viewModel, pageModifier)
        WorkbenchScreen.CallDetail -> CallManagementPage(state, viewModel, pageModifier)
        WorkbenchScreen.InsertAndStop -> InsertAndStopPage(state, viewModel, pageModifier)
        WorkbenchScreen.Approvals -> ApprovalManagementPage(state, viewModel, pageModifier)
        WorkbenchScreen.Permissions -> PermissionManagementPage(state, viewModel, pageModifier)
        WorkbenchScreen.Skills -> SkillManagementPage(state, viewModel, pageModifier)
        WorkbenchScreen.Plugins -> PluginMcpManagementPage(state, viewModel, pageModifier)
        WorkbenchScreen.CoreConnections -> ConnectionManagementPage(state, viewModel, pageModifier)
        WorkbenchScreen.InstallUpdate -> DeploymentPage(state, viewModel, pageModifier)
        WorkbenchScreen.ProjectsFiles -> ProjectFilesManagementPage(state, viewModel, pageModifier)
        WorkbenchScreen.LogsDiagnostics -> LogsDiagnosticsPage(state, viewModel, pageModifier)
        WorkbenchScreen.Settings -> SettingsManagementPage(state, viewModel, pageModifier)
    }
}

@Composable
private fun HomePage(state: WorkbenchUiState, viewModel: WorkbenchViewModel, modifier: Modifier) {
    LazyColumn(modifier, verticalArrangement = Arrangement.spacedBy(12.dp)) {
        item {
            PageHeader("Android 完整 Workbench", "原生 Compose 控制面；Core 与 Termux 仍是独立、可验证的外部组件。")
        }
        item {
            ElevatedCard(Modifier.fillMaxWidth()) {
                Column(Modifier.padding(16.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
                    Text("Core ${state.snapshot.coreHealth.name}", style = MaterialTheme.typography.titleLarge)
                    Text(state.snapshot.connectionMessage)
                    Text("版本 ${state.snapshot.coreVersion.ifBlank { "未知" }} · ${state.liveStatus}")
                    Text("期望节点状态：${state.settings.desiredNodeState}")
                }
            }
        }
        item {
            MetricRow(
                "对话" to state.snapshot.conversations.size,
                "任务" to state.snapshot.tasks.size,
                "调用" to state.snapshot.calls.size,
                "审批" to state.snapshot.approvals.size
            )
        }
        item {
            HorizontalActions(
                listOf(
                    "任务中心" to { viewModel.navigate(WorkbenchScreen.Tasks) },
                    "安装与更新" to { viewModel.navigate(WorkbenchScreen.InstallUpdate) },
                    "Core 连接" to { viewModel.navigate(WorkbenchScreen.CoreConnections) },
                    "权限" to { viewModel.navigate(WorkbenchScreen.Permissions) }
                )
            )
        }
        item {
            InfoCard(
                "边界说明",
                "关闭本页面不会停止 Core。暂停守护只暂停健康检查；“停止 Core”会先写入 desired=stopped，再交给 Termux 执行。"
            )
        }
        if (state.fixture) item { InfoCard("测试数据", "当前是显式 CI fixture，不代表真实设备或 Core 成功状态。") }
    }
}

@Composable
private fun ActivityPage(state: WorkbenchUiState, viewModel: WorkbenchViewModel, modifier: Modifier) {
    LazyColumn(modifier, verticalArrangement = Arrangement.spacedBy(10.dp)) {
        item { PageHeader("活动与调用", state.liveStatus) }
        item { SectionTitle("活动流") }
        if (state.snapshot.activity.isEmpty()) item { EmptyCard("暂无活动事件或活动端点不可用") }
        lazyItems(state.snapshot.activity.take(50), key = { "event-${it.id}" }) { WorkbenchItemCard(it) {} }
        item { SectionTitle("调用") }
        if (state.snapshot.calls.isEmpty()) item { EmptyCard("暂无调用") }
        lazyItems(state.snapshot.calls, key = { "call-${it.id}" }) { item ->
            WorkbenchItemCard(item, selected = item.id == state.selectedCallId) { viewModel.selectCall(item) }
        }
    }
}

@Composable
private fun InsertAndStopPage(state: WorkbenchUiState, viewModel: WorkbenchViewModel, modifier: Modifier) {
    val text = state.insertionDraft
    val selected = state.snapshot.conversations.firstOrNull { it.id == state.selectedConversationId }
    val bytes = text.toByteArray(StandardCharsets.UTF_8).size
    LazyColumn(modifier, verticalArrangement = Arrangement.spacedBy(10.dp)) {
        item { PageHeader("插入与停止", "插入有效窗 5 分钟；入口和停止资格使用最近 3 分钟；回执等待 30 秒。") }
        if (selected == null) {
            item { EmptyCard("请先选择对话") }
            lazyItems(state.snapshot.conversations, key = { "choose-${it.id}" }) { WorkbenchItemCard(it) { viewModel.selectConversation(it) } }
        } else {
            item { SelectedCard(selected) }
            item {
                OutlinedTextField(
                    value = text,
                    onValueChange = viewModel::setInsertionDraft,
                    modifier = Modifier.fillMaxWidth().testTag("insertion-text"),
                    label = { Text("补充要求") },
                    supportingText = { Text("$bytes / 8192 UTF-8 字节") },
                    minLines = 4
                )
            }
            item {
                HorizontalActions(
                    listOf(
                        "发送插入" to {
                            viewModel.sendInsertion(text)
                        },
                        "停止对话" to viewModel::terminateConversation,
                        "刷新回执" to viewModel::refresh
                    ),
                    enabled = !state.loading && !state.actionBusy
                )
            }
            item { SectionTitle("插入回执") }
            if (state.snapshot.insertions.isEmpty()) item { EmptyCard("当前对话没有插入记录") }
            lazyItems(state.snapshot.insertions, key = { "insertion-${it.id}" }) { insertion ->
                Card(Modifier.fillMaxWidth()) {
                    Column(Modifier.padding(12.dp), verticalArrangement = Arrangement.spacedBy(6.dp)) {
                        Text(insertion.title, maxLines = 3, overflow = TextOverflow.Ellipsis)
                        Text(insertion.status.ifBlank { "状态未知" })
                        Row {
                            TextButton(onClick = { viewModel.insertionAction(insertion.id, "retry") }) { Text("有限重投") }
                            TextButton(onClick = { viewModel.insertionAction(insertion.id, "cancel") }) { Text("撤回") }
                        }
                    }
                }
            }
        }
    }
}

@Composable
private fun LogsDiagnosticsPage(state: WorkbenchUiState, viewModel: WorkbenchViewModel, modifier: Modifier) {
    LazyColumn(modifier, verticalArrangement = Arrangement.spacedBy(10.dp)) {
        item { PageHeader("日志与诊断", "敏感值不展示；Termux 诊断包保留在其私有目录，需用户明确导出。") }
        item { InfoCard("实时流", state.liveStatus) }
        item {
            HorizontalActions(
                listOf(
                    "刷新快照" to viewModel::refresh,
                    "生成 Termux 诊断包" to { viewModel.runTermux("export_diagnostics") }
                )
            )
        }
        item { SectionTitle("调用摘要") }
        lazyItems(state.snapshot.calls.take(30), key = { "diag-call-${it.id}" }) { WorkbenchItemCard(it) { viewModel.selectCall(it) } }
        item { SectionTitle("Termux 操作摘要") }
        lazyItems(state.operations.take(30), key = { "diag-op-${it.operationId}" }) { OperationCard(it) }
    }
}

@Composable
private fun WorkbenchItemCard(item: WorkbenchItem, selected: Boolean = false, onClick: () -> Unit) {
    ElevatedCard(
        modifier = Modifier.fillMaxWidth().clickable(onClick = onClick),
    ) {
        ListItem(
            headlineContent = { Text(item.title, maxLines = 2, overflow = TextOverflow.Ellipsis) },
            supportingContent = {
                Column {
                    if (item.subtitle.isNotBlank()) Text(item.subtitle, maxLines = 2, overflow = TextOverflow.Ellipsis)
                    if (item.metadata.isNotBlank()) Text(item.metadata, maxLines = 1, overflow = TextOverflow.Ellipsis)
                }
            },
            trailingContent = { Text(if (selected) "已选" else item.status.ifBlank { "—" }) }
        )
    }
}

@Composable
private fun SelectedCard(item: WorkbenchItem, content: @Composable (() -> Unit)? = null) {
    ElevatedCard(Modifier.fillMaxWidth()) {
        Column(Modifier.padding(14.dp), verticalArrangement = Arrangement.spacedBy(6.dp)) {
            Text(item.title, style = MaterialTheme.typography.titleMedium)
            Text(item.id, style = MaterialTheme.typography.bodySmall)
            if (item.subtitle.isNotBlank()) Text(item.subtitle)
            if (item.status.isNotBlank()) Text("状态：${item.status}")
            content?.invoke()
        }
    }
}

@Composable
private fun OperationCard(operation: BridgeOperation) {
    Card(Modifier.fillMaxWidth()) {
        Column(Modifier.padding(12.dp), verticalArrangement = Arrangement.spacedBy(4.dp)) {
            Text("${operation.operation} · ${operation.phase}", fontWeight = FontWeight.SemiBold)
            Text(operation.operationId, style = MaterialTheme.typography.bodySmall)
            if (operation.message.isNotBlank()) Text(operation.message)
            if (operation.stdoutTruncated || operation.stderrTruncated) Text("Termux 输出已截断")
        }
    }
}

@Composable
private fun PageHeader(title: String, subtitle: String) {
    Column(verticalArrangement = Arrangement.spacedBy(4.dp)) {
        Text(title, style = MaterialTheme.typography.headlineSmall, fontWeight = FontWeight.SemiBold)
        Text(subtitle, style = MaterialTheme.typography.bodyMedium)
        HorizontalDivider(Modifier.padding(top = 4.dp))
    }
}

@Composable
private fun SectionTitle(value: String) {
    Text(value, style = MaterialTheme.typography.titleMedium, fontWeight = FontWeight.SemiBold)
}

@Composable
private fun EmptyCard(message: String) {
    Card(Modifier.fillMaxWidth()) { Text(message, Modifier.padding(16.dp)) }
}

@Composable
private fun InfoCard(title: String, body: String) {
    Card(Modifier.fillMaxWidth()) {
        Column(Modifier.padding(14.dp), verticalArrangement = Arrangement.spacedBy(6.dp)) {
            Text(title, fontWeight = FontWeight.SemiBold)
            Text(body)
        }
    }
}

@Composable
private fun MetricRow(vararg values: Pair<String, Int>) {
    Row(Modifier.fillMaxWidth().horizontalScroll(rememberScrollState()), horizontalArrangement = Arrangement.spacedBy(8.dp)) {
        values.forEach { (label, value) ->
            ElevatedCard(Modifier.width(112.dp)) {
                Column(Modifier.padding(12.dp)) {
                    Text(value.toString(), style = MaterialTheme.typography.headlineMedium)
                    Text(label)
                }
            }
        }
    }
}

@Composable
private fun HorizontalActions(actions: List<Pair<String, () -> Unit>>, enabled: Boolean = true) {
    Row(Modifier.fillMaxWidth().horizontalScroll(rememberScrollState()), horizontalArrangement = Arrangement.spacedBy(8.dp)) {
        actions.forEach { (label, action) -> OutlinedButton(onClick = action, enabled = enabled) { Text(label) } }
    }
}
