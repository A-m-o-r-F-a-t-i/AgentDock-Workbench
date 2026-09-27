package dev.agentdock.workbench.data

import org.json.JSONArray
import org.json.JSONObject

/** Immutable request intent. Core validates all state transitions and revision checks. */
data class CoreCommand(val title: String, val path: String, val bodyText: String, val consequence: String) {
    fun body() = JSONObject(bodyText)
}

data class CallFilter(
    val view: String = "active", val search: String = "", val status: String = "",
    val conversation: String = "", val task: String = "", val parent: String = "",
    val unattributed: Boolean = false, val before: Long = 0, val after: Long = 0
) {
    fun path(): String {
        require(view in setOf("active", "archived", "isolated", "trash", "all"))
        require(search.length <= 512 && status.length <= 80 && before >= 0 && after >= 0)
        val query = linkedMapOf("view" to view, "search" to search, "status" to status,
            "limit" to "100", "include_output" to "false", "top_level" to parent.isEmpty().toString())
        if (conversation.isNotBlank()) query["conversation_id"] = ManagementContract.id(conversation)
        if (task.isNotBlank()) query["task_id"] = ManagementContract.id(task)
        if (parent.isNotBlank()) query["parent_call_id"] = ManagementContract.id(parent)
        if (unattributed) query["unattributed"] = "true"
        if (before > 0) query["before"] = before.toString()
        if (after > 0) query["after"] = after.toString()
        return "/internal/runtime/calls?" + query.entries.joinToString("&") { "${it.key}=${ManagementContract.encode(it.value)}" }
    }
}

data class PermissionDraft(
    val scope: String,
    val scopeId: String,
    val revision: Long,
    val mode: String,
    val confirmFull: Boolean,
    val customEnabled: Boolean,
    val inheritSettings: Boolean,
    val filesystem: String,
    val network: String,
    val boundary: String,
    val approvalMode: String,
    val reviewer: String,
    val granularFileWrites: Boolean,
    val granularCommands: Boolean,
    val granularNetwork: Boolean,
    val granularMcp: Boolean,
    val granularManagement: Boolean,
    val granularOther: Boolean
)

object WorkbenchCommands {
    private const val PREFIX = "/internal/runtime"
    private fun command(title: String, path: String, body: JSONObject, consequence: String): CoreCommand {
        require(body.toString().toByteArray(Charsets.UTF_8).size <= 64 * 1024)
        return CoreCommand(title, PREFIX + path, body.toString(), consequence)
    }
    fun workspace(name: String, root: String, project: String, create: Boolean, id: String = "", revision: Long = 0,
                  artifactRoot: String = "", scratchRoot: String = "", cacheRoot: String = ""): CoreCommand {
        require(name.isNotBlank() && name.length <= 256 && root.startsWith('/') && root.length <= 4096)
        require(project.length <= 256)
        val body = JSONObject().put("action", "register").put("name", name).put("root", root)
            .put("project", project).put("runtime", "unix").put("kind", "directory").put("create_root", create)
        if (id.isNotEmpty()) {
            require(revision > 0)
            body.put("workspace_id", ManagementContract.id(id)).put("expected_revision", revision)
        }
        for ((key, value) in mapOf("artifact_root" to artifactRoot, "scratch_root" to scratchRoot, "cache_root" to cacheRoot)) {
            if (value.isNotBlank()) { require(value.startsWith('/') && value.length <= 4096); body.put(key, value) }
        }
        return command(if (id.isEmpty()) "登记工作区" else "更新工作区", "/workspaces", body,
            if (create) "Core 将创建明确指定的目录并登记工作区。不会改变其他运行会话的绑定。" else "仅登记或按修订号更新该路径。已有会话和工程文件保持原有归属。")
    }
    fun workspaceResolve(id: String, path: String): CoreCommand {
        require(path.length <= 4096)
        return command("校验工作区路径", "/workspaces", JSONObject().put("action", "resolve")
            .put("workspace_id", ManagementContract.id(id)).put("target_kind", "source").put("path", path), "只请求 Core 解析工作区路径，不运行命令或修改文件。")
    }
    fun task(action: String, id: String, thread: String = "", summary: String = "", step: String = "", status: String = ""): CoreCommand {
        require(action in setOf("resume", "block", "cancel", "checkpoint", "thread_switch", "thread_create", "thread_close"))
        require(summary.length <= 4096)
        val body = JSONObject().put("action", action).put("task_id", ManagementContract.id(id))
        if (thread.isNotEmpty()) body.put("thread_id", ManagementContract.id(thread))
        if (summary.isNotBlank()) body.put("summary", summary)
        if (action == "checkpoint") {
            require(step.isNotBlank() && status in setOf("pending", "in_progress", "completed", "blocked"))
            body.put("step_id", ManagementContract.id(step)).put("status", status)
        }
        if (action == "thread_create") { require(summary.isNotBlank()); body.put("title", summary) }
        if (action == "cancel" || action == "block") require(summary.isNotBlank())
        return command("任务 $action", "/tasks", body, "仅提交所选任务与分支的管理请求。已运行命令不重放，实际状态以 Core 回读为准。")
    }
    fun taskLifecycle(action: String, id: String, summary: String = ""): CoreCommand {
        require(action in setOf("resume", "block", "cancel", "complete"))
        if (action in setOf("block", "cancel")) require(summary.isNotBlank())
        require(summary.length <= 4096)
        val body = JSONObject().put("action", action).put("task_id", ManagementContract.id(id))
        if (summary.isNotBlank()) body.put("summary", summary)
        return command(
            when (action) {
                "resume" -> "恢复任务"
                "block" -> "标记任务受阻"
                "cancel" -> "取消任务"
                else -> "完成任务"
            },
            "/tasks",
            body,
            "只改变所选任务的持久生命周期状态；已开始的调用不会被重放，最终状态以 Core 回读为准。"
        )
    }
    fun taskReview(id: String, status: String, summary: String, verified: List<String>, risks: List<String>): CoreCommand {
        require(status in setOf("pass", "failed") && summary.isNotBlank() && summary.length <= 4096)
        require(verified.size <= 100 && risks.size <= 100 && verified.all { it.isNotBlank() && it.length <= 2048 } && risks.all { it.isNotBlank() && it.length <= 2048 })
        require(status != "pass" || verified.isNotEmpty())
        require(status != "failed" || risks.isNotEmpty())
        val body = JSONObject().put("action", "final_review").put("task_id", ManagementContract.id(id))
            .put("status", status).put("summary", summary)
        if (verified.isNotEmpty()) body.put("verified", JSONArray(verified))
        if (risks.isNotEmpty()) body.put("risks", JSONArray(risks))
        return command("提交任务最终审查", "/tasks", body,
            "保存所选任务的审查结论和明确证据；不自动完成任务，也不执行任何工具。")
    }
    fun taskThread(
        action: String,
        taskId: String,
        threadId: String = "",
        title: String = "",
        summary: String = "",
        nextAction: String = "",
        currentStepId: String = "",
        completedStepIds: List<String> = emptyList()
    ): CoreCommand {
        require(action in setOf("thread_create", "thread_switch", "thread_checkpoint", "thread_block", "thread_resume", "thread_close"))
        require(summary.length <= 4096 && nextAction.length <= 4096 && title.length <= 512)
        require(completedStepIds.size <= 12 && completedStepIds.distinct().size == completedStepIds.size)
        completedStepIds.forEach(ManagementContract::id)
        val body = JSONObject().put("action", action).put("task_id", ManagementContract.id(taskId))
        if (threadId.isNotBlank()) body.put("thread_id", ManagementContract.id(threadId))
        if (title.isNotBlank()) body.put("title", title)
        if (summary.isNotBlank()) body.put("summary", summary)
        if (nextAction.isNotBlank()) body.put("next_action", nextAction)
        if (currentStepId.isNotBlank()) body.put("current_step_id", ManagementContract.id(currentStepId))
        if (completedStepIds.isNotEmpty()) body.put("completed_step_ids", JSONArray(completedStepIds))
        require(action != "thread_create" || title.isNotBlank())
        require(action == "thread_create" || threadId.isNotBlank())
        require(action != "thread_checkpoint" || summary.isNotBlank() || nextAction.isNotBlank() || currentStepId.isNotBlank() || completedStepIds.isNotEmpty())
        return command("任务分支 $action", "/tasks", body,
            "仅修改此任务内的明确分支；切换分支只影响后续延续，已运行调用保留原绑定。")
    }
    fun createTask(title: String, goal: String, conditions: List<String>, workspaceId: String): CoreCommand {
        require(title.isNotBlank() && title.length <= 512 && goal.isNotBlank() && goal.length <= 4096)
        require(conditions.isNotEmpty() && conditions.size <= 30 && conditions.all { it.isNotBlank() && it.length <= 2048 })
        val body = JSONObject().put("action", "create").put("title", title)
            .put("goal", goal).put("completion_conditions", JSONArray(conditions))
        if (workspaceId.isNotBlank()) body.put("workspace_id", ManagementContract.id(workspaceId))
        return command("创建持久任务", "/tasks", body,
            "在选定工作区创建独立持久任务；不会复制已有任务或自动执行工具。")
    }
    fun lifecycle(conversation: String, action: String): CoreCommand {
        require(action in setOf("terminate", "resume"))
        return command(if (action == "terminate") "终止对话" else "恢复对话", "/conversations/${ManagementContract.id(conversation)}/$action", JSONObject().put("confirm", true),
            if (action == "terminate") "先阻止新执行、取消待审批并停止运行调用。历史保持可读；部分停止失败须根据回执核对。" else "重新允许此对话提交请求，已经取消的调用不会重放。")
    }
    fun link(conversation: String, task: String): CoreCommand = command("关联已有任务", "/conversations/${ManagementContract.id(conversation)}/link-task",
        JSONObject().put("task_id", ManagementContract.id(task)), "仅建立关联，不切换正在执行的任务。")
    fun currentTask(conversation: String, task: String, thread: String, revision: Long): CoreCommand {
        require(revision >= 0)
        val body = JSONObject().put("task_id", if (task.isEmpty()) "" else ManagementContract.id(task))
            .put("task_thread_id", if (thread.isEmpty()) "" else ManagementContract.id(thread)).put("binding_revision", revision)
        return command(if (task.isEmpty()) "解除当前任务绑定" else "切换当前任务", "/conversations/${ManagementContract.id(conversation)}/current-task", body,
            "按刚读取的 binding_revision 修改此对话的后续执行绑定。已经运行的调用保留原绑定。")
    }
    fun callBatch(ids: List<String>, action: String, confirmed: Boolean = false): CoreCommand {
        require(ids.size in 1..200 && ids.distinct().size == ids.size)
        ids.forEach(ManagementContract::id)
        require(action in setOf("archive", "unarchive", "isolate", "unisolate", "trash", "restore", "delete"))
        require(action != "delete" || confirmed)
        val body = JSONObject().put("ids", JSONArray(ids)).put("action", action)
        if (action == "delete") body.put("confirm_permanent", true)
        return command("管理 ${ids.size} 条调用", "/calls/batch", body, "只处理已经冻结的调用 ID。运行中或待审批对象由 Core 判定是否跳过，项目文件保留。")
    }
    fun stopCall(id: String) = command("停止所选调用", "/calls/${ManagementContract.id(id)}/stop", JSONObject(), "只停止此调用，保留历史；不会自动重试命令。")
    fun approval(id: String, approve: Boolean, workspace: Boolean) = command(if (approve) "批准请求" else "拒绝请求",
        "/approvals/${ManagementContract.id(id)}/${if (approve) "approve" else "reject"}", JSONObject().put("allow_workspace", workspace),
        if (workspace && approve) "按审批详情中的工作区范围授予授权。显式禁止规则仍由 Core 执行。" else "只处理此审批。过期、已处理和版本冲突由 Core 拒绝。")
    fun permission(value: PermissionDraft): CoreCommand {
        require(value.scope in setOf("global", "workspace", "conversation"))
        require(value.revision > 0 && value.mode in setOf("readonly", "rules", "full"))
        require(value.scope == "global" && value.scopeId.isEmpty() || value.scope != "global" && value.scopeId.isNotEmpty())
        if (value.scope != "global") ManagementContract.id(value.scopeId)
        require(value.scope != "conversation" || value.mode != "full")
        require(value.mode != "full" || value.confirmFull)
        require(value.scope == "workspace" || !value.inheritSettings)
        val body = JSONObject().put("scope", value.scope).put("expected_revision", value.revision).put("mode", value.mode)
        if (value.scope != "global") body.put("scope_id", value.scopeId)
        if (value.mode == "full") body.put("confirm_full", true)
        if (value.scope != "conversation") {
            if (value.inheritSettings) {
                body.put("inherit_settings", true)
            } else {
                require(value.filesystem in setOf("deny", "read", "write"))
                require(value.network in setOf("deny", "allow"))
                require(value.boundary in setOf("none", "workspace"))
                require(value.approvalMode in setOf("on-request", "never", "granular"))
                require(value.reviewer in setOf("user", "auto_review"))
                val approval = JSONObject().put("mode", value.approvalMode)
                if (value.approvalMode == "granular") approval.put("granular", JSONObject()
                    .put("file_writes", value.granularFileWrites).put("commands", value.granularCommands)
                    .put("network", value.granularNetwork).put("mcp", value.granularMcp)
                    .put("management", value.granularManagement).put("other", value.granularOther))
                body.put("custom_permissions_enabled", value.customEnabled)
                    .put("settings", JSONObject()
                        .put("permission_profile", JSONObject().put("filesystem", value.filesystem)
                            .put("network", value.network).put("sandbox_boundary", value.boundary))
                        .put("approval_policy", approval).put("approval_reviewer", value.reviewer))
            }
        }
        val target = if (value.scope == "global") "全局" else "${value.scope}:${value.scopeId}"
        return command("保存权限 · $target", "/permissions", body,
            "按刚读取的 revision 更新明确作用域。Full 需要本次显式确认；该设置只约束工具准入，不提升 Android、Termux 或操作系统权限。")
    }
    fun skill(reference: String, enable: Boolean): CoreCommand {
        require(reference.isNotBlank() && reference.length <= 2048 && reference.none { it.code < 32 })
        return command(if (enable) "启用 Skill" else "停用 Skill", "/skills", JSONObject().put("action", if (enable) "enable" else "disable").put("skill", reference), "改变此精确 Skill 的启用状态，不编辑源文件。")
    }
    fun plugin(action: String, name: String = "", source: String = "", memberType: String = "", member: String = ""): CoreCommand {
        require(action in setOf("validate", "install", "update", "remove", "enable", "disable", "heavy_enable", "heavy_disable", "member_enable", "member_disable"))
        val body = JSONObject().put("action", action)
        if (name.isNotBlank()) { require(name.length <= 256 && name.none { it.code < 32 }); body.put("name", name) }
        if (action in setOf("validate", "install", "update")) {
            require(source.isNotBlank() && source.length <= 4096 && source.none { it.code < 32 })
            body.put("source", source)
        }
        if (action.startsWith("member_")) {
            require(memberType in setOf("skill", "mcp_server") && member.isNotBlank())
            body.put("member_type", memberType).put("member", member)
        }
        return command("插件 $action", "/plugins", body, "由 Core 的插件管理服务执行。安装或更新前须先验证来源并核对回执；移除不删除用户工程。")
    }
    fun mcp(action: String, name: String, transport: String = "", url: String = "", executable: String = "", args: List<String> = emptyList(), cwd: String = "", timeout: Int = 30000): CoreCommand {
        require(action in setOf("add", "remove", "enable", "disable", "refresh", "env_list"))
        require(name.isNotBlank() && name.length <= 128 && name.none { it.code < 32 || it == '/' })
        val body = JSONObject().put("action", action).put("name", name)
        if (action == "add") {
            require(transport in setOf("streamable_http", "stdio") && timeout in 1..300000)
            body.put("transport", transport).put("timeout_ms", timeout)
            if (transport == "streamable_http") {
                val uri = java.net.URI(url)
                require(uri.scheme in setOf("https", "http") && uri.host != null && uri.userInfo == null && uri.fragment == null && uri.query == null)
                require(uri.scheme == "https" || uri.host in setOf("127.0.0.1", "localhost", "[::1]"))
                body.put("url", url)
            } else {
                require(executable.isNotBlank() && executable.length <= 4096 && args.size <= 64 && args.all { it.length <= 4096 })
                body.put("command", executable).put("args", JSONArray(args))
                if (cwd.isNotBlank()) { require(cwd.startsWith('/')); body.put("cwd", cwd) }
            }
        }
        return command("MCP $action", "/mcp", body, "只修改选定 MCP 的已声明配置。不会将请求头秘密保存到普通配置；环境秘密通过独立受控输入管理。")
    }
    fun mcpEnvironment(name: String, key: String, value: String? = null): CoreCommand {
        require(name.isNotBlank() && name.length <= 128 && name.none { it.code < 32 || it == '/' })
        require(Regex("^[A-Za-z_][A-Za-z0-9_]{0,127}$").matches(key))
        require(value == null || value.toByteArray(Charsets.UTF_8).size <= 32 * 1024)
        val action = if (value == null) "env_unset" else "env_set"
        val body = JSONObject().put("action", action).put("name", name).put("key", key)
        if (value != null) body.put("value", value)
        return command(if (value == null) "删除 MCP 环境变量" else "保存 MCP 环境变量", "/mcp", body,
            "值写入 Core 的隔离环境存储，响应不会回显秘密。Android 不在页面状态、DataStore 或日志中保存该值。")
    }
    fun display(revision: Long, enabled: Boolean, limit: Int, mcpUi: Boolean): CoreCommand {
        require(revision > 0 && limit in 1000..100000)
        return command("保存 Core 输出设置", "/execution/display", JSONObject().put("expected_revision", revision)
            .put("chatgpt_mcp_ui_enabled", mcpUi).put("tool_output", JSONObject().put("enabled", enabled).put("max_chars", limit)),
            "按 Core 修订号更新输出与 MCP UI 设置。Android 本地显示偏好独立保存，旧请求不会扩大服务端上限。")
    }
}

object CorePages {
    fun rows(value: JSONObject, key: String, maximum: Int = 200): List<JSONObject> {
        val array = value.optJSONArray(key) ?: error("Core 响应缺少 $key 数组；不按空列表处理")
        require(array.length() <= maximum) { "Core 页大小超过声明上限" }
        return (0 until array.length()).map { array.getJSONObject(it) }
    }
    fun next(value: JSONObject, current: Long, key: String, descending: Boolean = false): Long? {
        if (!value.optBoolean("has_more")) return null
        val raw = value.opt(key)
        require(raw is Number) { "Core 分页游标缺失" }
        val next = raw.toLong()
        require(next >= 0 && next != current && (current == 0L || if (descending) next < current else next > current)) { "Core 分页游标未前进" }
        return next
    }
    fun text(value: JSONObject, key: String) = ManagementContract.text(value, key)
}
