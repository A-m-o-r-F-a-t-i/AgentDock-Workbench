package dev.agentdock.workbench.ui

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.material3.Button
import androidx.compose.material3.Card
import androidx.compose.material3.Checkbox
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

@Composable
fun ApprovalManagementPage(state: WorkbenchUiState, model: WorkbenchViewModel, modifier: Modifier) {
    val resources by model.resources.state.collectAsStateWithLifecycle()
    val prompt = rememberCommandPrompt(model)
    var status by rememberSaveable { mutableStateOf("pending") }
    var offset by rememberSaveable { mutableStateOf(0) }
    var selected by rememberSaveable { mutableStateOf(state.selectedApprovalId) }
    var allowWorkspace by rememberSaveable(selected) { mutableStateOf(false) }

    fun path(): String = buildString {
        append("/internal/runtime/approvals?limit=100&offset=").append(offset)
        if (status.isNotBlank()) append("&status=").append(ManagementContract.encode(status))
    }
    fun reload() = model.resources.read("approvals", path())
    fun select(id: String) {
        selected = id
        model.resources.read("approval-detail", "/internal/runtime/approvals/${ManagementContract.id(id)}")
    }
    LaunchedEffect(state.settings.endpoint, state.settings.remoteEndpointEnabled, status, offset) { reload() }
    LaunchedEffect(selected) { if (selected.isNotBlank()) select(selected) }

    val list = resources.views["approvals"]
    val rows = remember(list?.data) { runCatching { list?.data?.let { CorePages.rows(it, "approvals") }.orEmpty() } }
    val total = list?.data?.optInt("total", -1) ?: -1
    val hasMore = list?.data?.optBoolean("has_more") == true
    val nextOffset = list?.data?.optInt("next_offset", -1) ?: -1
    val detailEnvelope = resources.views["approval-detail"]?.data
    val detail = detailEnvelope?.optJSONObject("approval") ?: detailEnvelope
    val pending = detail?.optString("status") == "pending"
    val workspace = detail?.optString("workspace_id").orEmpty()

    LazyColumn(modifier, verticalArrangement = Arrangement.spacedBy(10.dp)) {
        item {
            Text("审批与历史", style = MaterialTheme.typography.headlineSmall)
            Text("审批绑定固定 CallID 和权限修订号；已过期或已处理请求不会重新派发。")
            ResourceActions {
                listOf(
                    "pending" to "待处理",
                    "approved" to "已批准",
                    "rejected" to "已拒绝",
                    "expired" to "已过期",
                    "cancelled" to "已取消",
                    "" to "全部"
                ).forEach { (value, label) ->
                    FilterChip(status == value, { status = value; offset = 0 }, label = { Text(label) })
                }
            }
            ResourceActions {
                TextButton(onClick = ::reload) { Text("刷新") }
                Text("偏移 $offset · 总计 ${if (total >= 0) total else "未知"}", Modifier.padding(top = 12.dp))
            }
            ResourceFeedback(list, ::reload)
            rows.exceptionOrNull()?.let { Text(it.message.orEmpty()) }
            if (list?.data != null && rows.getOrDefault(emptyList()).isEmpty()) Text("当前筛选没有审批记录。")
        }
        items(rows.getOrDefault(emptyList()), key = { it.optString("approval_id") }) { row ->
            val id = row.optString("approval_id")
            Card(Modifier.fillMaxWidth()) {
                Row(Modifier.padding(12.dp), horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                    Column(Modifier.weight(1f)) {
                        Text(row.optString("operation", row.optString("tool", id)), style = MaterialTheme.typography.titleMedium)
                        ResourceObject(row, listOf("approval_id", "status", "tool", "action", "call_id", "workspace_id", "approval_reviewer", "policy_revision", "created_at", "expires_at", "decided_at", "decided_by", "summary"))
                    }
                    TextButton(onClick = { select(id) }) { Text(if (selected == id) "刷新详情" else "详情") }
                }
            }
        }
        item {
            ResourceActions {
                TextButton(onClick = { offset = (offset - 100).coerceAtLeast(0) }, enabled = offset > 0) { Text("上一页") }
                TextButton(onClick = {
                    if (hasMore && nextOffset > offset) offset = nextOffset
                    else model.showNotice("审批分页游标缺失或未前进。")
                }, enabled = hasMore) { Text("下一页") }
            }
        }
        if (selected.isNotBlank()) item {
            ResourceSection("审批详情 $selected") {
                ResourceFeedback(resources.views["approval-detail"]) { select(selected) }
                detail?.let {
                    ResourceObject(it, listOf("approval_id", "status", "tool", "action", "operation", "scope_description", "reason", "mode", "call_id", "workspace_id", "task_id", "thread_id", "approval_reviewer", "review_decision", "review_reason", "policy_revision", "workspace_revision", "created_at", "expires_at", "reviewed_at", "decided_at", "decided_by", "summary"))
                }
                detailEnvelope?.optString("fixed_request")?.takeIf(String::isNotBlank)?.let {
                    Text("固定请求（已脱敏）", style = MaterialTheme.typography.titleSmall)
                    Text(it.take(32_768))
                }
                detailEnvelope?.optJSONObject("rule_preview")?.let {
                    Text("工作区规则预览", style = MaterialTheme.typography.titleSmall)
                    ResourceObject(it, listOf("tool", "action", "workspace_id", "effect", "reason"))
                }
                if (pending) {
                    if (workspace.isNotBlank()) Row {
                        Checkbox(allowWorkspace, { allowWorkspace = it })
                        Text("同时允许此工作区的同类操作", Modifier.padding(top = 12.dp))
                    }
                    ResourceActions {
                        Button(onClick = {
                            prompt.ask({ WorkbenchCommands.approval(selected, true, allowWorkspace) }) { reload(); select(selected) }
                        }, enabled = !resources.actionBusy) { Text(if (allowWorkspace) "批准并保存工作区规则" else "仅本次批准") }
                        OutlinedButton(onClick = {
                            prompt.ask({ WorkbenchCommands.approval(selected, false, false) }) { reload(); select(selected) }
                        }, enabled = !resources.actionBusy) { Text("拒绝") }
                    }
                } else if (detail != null) {
                    Text("此审批已有终态，只读展示历史，不提供再次派发按钮。")
                }
            }
        }
        item { ResourceReceipt(resources.actionResult) }
    }
}
