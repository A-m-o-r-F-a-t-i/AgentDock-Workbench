package dev.agentdock.workbench.ui

import androidx.compose.foundation.horizontalScroll
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.rememberScrollState
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.unit.dp
import dev.agentdock.workbench.data.CoreCommand
import dev.agentdock.workbench.data.ManagementContract
import org.json.JSONObject

class CommandPrompt(private val model: WorkbenchViewModel) {
    var pending by mutableStateOf<CoreCommand?>(null)
    var after: ((JSONObject) -> Unit)? = null
    fun ask(builder: () -> CoreCommand, accepted: (JSONObject) -> Unit = {}) {
        try { pending = builder(); after = accepted }
        catch (error: Exception) { model.showNotice(ManagementContract.failure(error).ifBlank { "请核对必填字段及参数范围。" }) }
    }
}

@Composable
fun rememberCommandPrompt(model: WorkbenchViewModel): CommandPrompt {
    val prompt = remember(model) { CommandPrompt(model) }
    prompt.pending?.let { command ->
        AlertDialog(onDismissRequest = { prompt.pending = null },
            title = { Text(command.title) },
            text = { Column(verticalArrangement = Arrangement.spacedBy(8.dp)) {
                Text(command.consequence)
                TechnicalDetails("请求详情") { Text("目标：${command.path}") }
                Text("提交后，关闭窗口不会撤销操作。")
            } },
            confirmButton = { TextButton(onClick = {
                val callback = prompt.after
                prompt.pending = null; prompt.after = null
                model.resources.execute(command) { callback?.invoke(it) }
            }, modifier = Modifier.testTag("confirm-core-action")) { Text("确认") } },
            dismissButton = { TextButton(onClick = { prompt.pending = null; prompt.after = null }) { Text("取消") } })
    }
    return prompt
}

@Composable
fun ResourceFeedback(view: ResourceView?, retry: () -> Unit) {
    when {
        view == null || view.loading -> LinearProgressIndicator(Modifier.fillMaxWidth())
        view.error.isNotEmpty() -> Card(Modifier.fillMaxWidth()) {
            Column(Modifier.padding(12.dp)) {
                Text("读取失败", style = MaterialTheme.typography.titleMedium)
                Text(view.error)
                TextButton(onClick = retry) { Text("重新读取") }
            }
        }
    }
}

@Composable
fun ResourceSection(title: String, content: @Composable ColumnScope.() -> Unit) {
    ElevatedCard(Modifier.fillMaxWidth()) {
        Column(Modifier.padding(14.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
            Text(title, style = MaterialTheme.typography.titleMedium)
            content()
        }
    }
}

@Composable
fun ResourceField(label: String, value: String, change: (String) -> Unit, multiline: Boolean = false) {
    OutlinedTextField(value, change, label = { Text(label) }, modifier = Modifier.fillMaxWidth(),
        singleLine = !multiline, minLines = if (multiline) 3 else 1)
}

@Composable
fun ResourceActions(content: @Composable RowScope.() -> Unit) {
    Row(Modifier.fillMaxWidth().horizontalScroll(rememberScrollState()), horizontalArrangement = Arrangement.spacedBy(8.dp), content = content)
}

@Composable
fun ResourceObject(value: JSONObject, keys: List<String> = listOf("title", "name", "summary", "status", "workspace_id", "call_id", "parent_call_id", "tool_name", "display_command", "error_code", "error_summary", "created_at", "updated_at", "completed_at")) {
    val labels = mapOf("title" to "标题", "name" to "名称", "goal" to "目标", "summary" to "摘要", "message" to "说明",
        "status" to "状态", "tool_name" to "工具", "display_command" to "命令", "error_code" to "错误码", "error_summary" to "错误",
        "created_at" to "创建时间", "updated_at" to "更新时间", "completed_at" to "完成时间", "succeeded" to "成功", "failed" to "失败", "skipped" to "跳过")
    val entries = keys.mapNotNull { key ->
        val raw = value.opt(key)
        if (raw == null || raw == JSONObject.NULL || raw is JSONObject || raw is org.json.JSONArray || raw.toString().isBlank()) null
        else key to raw.toString().take(4096)
    }
    entries.filter { it.first in labels }.forEach { (key, text) ->
        Text("${labels[key]}：${if (key == "status") userStatus(text) else text}")
    }
    val technical = entries.filterNot { it.first in labels }
    if (technical.isNotEmpty()) TechnicalDetails { technical.forEach { (key, text) -> Text("$key：$text", style = MaterialTheme.typography.bodySmall) } }

}

@Composable
fun ResourceReceipt(value: JSONObject?) {
    if (value == null) return
    ResourceSection("最近操作结果") {
        ResourceObject(value, listOf(
            "status", "message", "ok", "succeeded", "skipped", "failed", "has_remaining_activity", "revision",
            "code", "error", "error_kind", "operation_status", "failure_class", "execution_performed",
            "effect_state", "retryable", "retry_safe"
        ))
        value.optJSONObject("required_action")?.let { action ->
            TechnicalDetails("服务端要求的下一动作") {
                ResourceObject(action, listOf("action", "category", "reason_code", "method", "message"))
            }
        }
        value.optJSONObject("details")?.let { details ->
            TechnicalDetails("错误详情") {
                ResourceObject(details, listOf(
                    "operation_status", "failure_class", "execution_performed", "effect_state", "retry_safe",
                    "current_revision", "expected_revision", "minimum", "maximum", "limit"
                ))
            }
        }
        value.optJSONArray("warnings")?.let { warnings ->
            for (index in 0 until minOf(warnings.length(), 100)) Text(warnings.optString(index).take(2048))
        }
        value.optJSONArray("items")?.let { items ->
            TechnicalDetails("逐项结果（${items.length()}）") {
                for (index in 0 until minOf(items.length(), 200)) items.optJSONObject(index)?.let {
                    Text("${it.optString("id")} · ${userStatus(it.optString("status"))} · ${it.optString("message").take(2048)}")
                }
            }
        }
    }
}
