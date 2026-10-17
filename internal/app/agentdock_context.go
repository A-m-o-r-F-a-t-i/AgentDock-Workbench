package app

import (
	"context"
	"fmt"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/uvwt/agentdock/internal/activity"
	"github.com/uvwt/agentdock/internal/agentinstructions"
	"github.com/uvwt/agentdock/internal/buildinfo"
	"github.com/uvwt/agentdock/internal/capabilityrouting"
	"github.com/uvwt/agentdock/internal/config"
	mcpclient "github.com/uvwt/agentdock/internal/mcp/client"
	pluginregistry "github.com/uvwt/agentdock/internal/plugin"
	"github.com/uvwt/agentdock/internal/snapshot"
	"github.com/uvwt/agentdock/internal/taskstate"
	toolmcp "github.com/uvwt/agentdock/internal/tool/mcp"
	tooltask "github.com/uvwt/agentdock/internal/tool/task"
	"github.com/uvwt/agentdock/internal/workspace"
)

func (r *Runtime) AgentDockContext(ctx context.Context) (Result, error) {
	return r.Call(ctx, "agentdock_context", map[string]any{})
}

// AgentDockLocalContext 仅供 Nexus Bridge 使用。它不读取 Nexus 统一管理的
// Workflow/Recall，避免 fleet 聚合时按节点重复回灌共享上下文。
func (r *Runtime) AgentDockLocalContext(ctx context.Context) (Result, error) {
	return r.agentDockContext(ctx, true, "")
}

func (r *Runtime) agentDockContext(ctx context.Context, nexusLocalOnly bool, workdir string) (Result, error) {
	ctx, cancel := context.WithTimeout(ctx, r.cfg.ContextBudget())
	defer cancel()
	started := time.Now()
	var selectedWorkspace *workspace.Record
	if !nexusLocalOnly {
		selected, selectedWorkdir, err := r.selectContextWorkspace(ctx, workdir)
		if err != nil {
			return nil, err
		}
		selectedWorkspace, workdir = &selected, selectedWorkdir
	}

	var instructions agentinstructions.Snapshot
	var instructionErr, directoryErr, skillErr, dynamicMCPErr, commonSkillErr, indexErr, gitErr, pluginIndexErr error
	var directory *pluginregistry.Directory
	var pluginInfo, skillInfo, commonInfo snapshot.Info
	var skills []capabilitySkillItem
	var dynamicMCP []capabilityDynamicMCPItem
	var commonSkills *capabilityCommonSkillIndex
	plugins := []capabilityPluginItem{}
	var index taskstate.TaskIndex
	var ruleElapsed, pluginElapsed, skillElapsed, mcpElapsed, commonElapsed, taskElapsed time.Duration
	var git workspace.GitSnapshot
	var gitElapsed, sessionElapsed time.Duration
	var running, pending int
	var sessionErr error
	var group sync.WaitGroup
	guard := func(failure *error, work func()) func() {
		return func() {
			defer func() {
				if recovered := recover(); recovered != nil {
					*failure = fmt.Errorf("context component panic: %v", recovered)
				}
			}()
			work()
		}
	}
	launch := func(failure *error, work func()) {
		group.Add(1)
		go func() { defer group.Done(); guard(failure, work)() }()
	}
	launch(&instructionErr, func() {
		start := time.Now()
		instructions, instructionErr = r.InstructionFiles(ctx, workdir)
		ruleElapsed = time.Since(start)
	})
	launch(&directoryErr, func() {
		start := time.Now()
		directory, pluginInfo, directoryErr = r.pluginStore.Snapshot(ctx)
		pluginElapsed = time.Since(start)
		if directory == nil && directoryErr == nil {
			directoryErr = fmt.Errorf("plugin snapshot returned no directory")
		}
		if directoryErr != nil || directory == nil {
			skillErr, dynamicMCPErr = directoryErr, directoryErr
			return
		}
		// Stage B depends only on the immutable plugin directory, not on Git,
		// task storage or instruction-file latency from the other Stage A nodes.
		var members sync.WaitGroup
		members.Add(3)
		go func() {
			defer members.Done()
			guard(&skillErr, func() {
				start := time.Now()
				skills, skillInfo, skillErr = r.contextSkillIndex(ctx, directory, nexusLocalOnly)
				skillElapsed = time.Since(start)
			})()
		}()
		go func() {
			defer members.Done()
			guard(&dynamicMCPErr, func() {
				start := time.Now()
				dynamicMCP, dynamicMCPErr = r.dynamicMCPCapabilityIndexContext(ctx, nexusLocalOnly, directory)
				mcpElapsed = time.Since(start)
			})()
		}()
		go func() {
			defer members.Done()
			if !nexusLocalOnly {
				guard(&pluginIndexErr, func() { plugins = pluginCapabilityIndexFromDirectory(directory) })()
			}
		}()
		members.Wait()
	})
	launch(&commonSkillErr, func() {
		start := time.Now()
		commonSkills, commonInfo, commonSkillErr = r.cachedCommonSkillIndex(ctx)
		commonElapsed = time.Since(start)
	})
	if !nexusLocalOnly {
		launch(&indexErr, func() {
			start := time.Now()
			indexCtx, stop := context.WithTimeout(ctx, 2*time.Second)
			defer stop()
			index, indexErr = r.taskTools.ContextIndexForWorkspace(indexCtx, selectedWorkspace.ID)
			taskElapsed = time.Since(start)
		})
		launch(&gitErr, func() {
			start := time.Now()
			git = workspace.ReadGitSnapshot(ctx, selectedWorkspace.Root)
			gitElapsed = time.Since(start)
		})
		launch(&sessionErr, func() {
			start := time.Now()
			running, pending, sessionErr = r.contextSessionCounts(ctx, activity.FromContext(ctx), selectedWorkspace.ID)
			sessionElapsed = time.Since(start)
		})
	}
	group.Wait()
	if instructionErr != nil {
		return nil, instructionErr
	}
	if directory == nil {
		directory = &pluginregistry.Directory{}
	}
	if skills == nil {
		skills = []capabilitySkillItem{}
	}
	if dynamicMCP == nil {
		dynamicMCP = []capabilityDynamicMCPItem{}
	}
	contextResult := capabilityContext{
		Workspace:         selectedWorkspace,
		Skills:            skills,
		CommonSkills:      commonSkills,
		Plugins:           plugins,
		DynamicMCP:        dynamicMCP,
		WorkflowTemplates: []capabilityTemplateItem{},
		Rules: []string{
			"AgentDock 设备内置工具与设备内动态 MCP 是两个独立命名空间。host_tools 中 scope=agentdock_builtin 的条目按 invoke_via=direct 直接调用，裸名称不得送入 mcp_tool_search 或 mcp_tool_inspect。dynamic_mcp 中 scope=agentdock_mcp 的服务使用 mcp_tool_list 读取已知目录、mcp_tool_search 定位能力、mcp_tool_inspect 读取所需 Schema，并以 server:tool 经 mcp_tool_call 调用。",
			"Skill 选择使用索引返回的 skill_ref/file；同名项是不同来源候选，保持各自作用域。common_skills.truncated=true 表示公共 Skill 索引不完整，不得把未列出的 Skill 判定为不存在。",
			"仅应用 instruction_files 中 status=loaded 的文件，并按返回顺序叠加；not_found、skipped 和 error 项不视为已应用。",
			"普通工具继承当前对话的 Workspace、Task 和 Thread。工作区外目标继续经过显式路径和权限检查；长任务使用 task_manage checkpoint 更新已有任务，不重复创建同一任务。",
		},
	}
	if hasExplicitLoadPlugin(directory) {
		contextResult.Rules = append(contextResult.Rules, capabilityrouting.ComputerUseRule)
	}
	if nexusLocalOnly && r.cfg.InstructionsFile == "" && strings.TrimSpace(r.cfg.Instructions) != "" {
		contextResult.Rules = append(contextResult.Rules, "Additional operator instructions:\n"+r.cfg.Instructions)
	}
	if nexusLocalOnly {
		// Keep the shared Bridge context contract unchanged. Device guidance travels
		// through its existing rules field, not a node-specific schema extension.
		if text := instructions.Text(); text != "" {
			contextResult.Rules = append(contextResult.Rules, text)
		}
	} else {
		contextResult.InstructionFiles = &instructions
		contextResult.Rules = append(contextResult.Rules, InsertionInstructions)
		if len(plugins) > 0 {
			contextResult.Rules = append(contextResult.Rules, "plugins 中的 Heavy/显式加载插件通过 plugin_load 展开；普通插件已启用成员直接见 skills/dynamic_mcp。")
		}

	}
	if !nexusLocalOnly {
		contextResult.BuiltinToolBundles = r.builtinToolBundleItems(ctx)
		contextResult.Rules = append(contextResult.Rules, "builtin_tool_bundles 已列出可用低频工具包。仅在需要包内工具时 capability_load action=load 一次，随后刷新 Host tools/list 再调用目标工具；重复加载幂等，不改变权限或插件状态。")
		// runtime 只保留模型操作主机所需的稳定环境事实；Nexus Bridge 已通过 Hello 持有这些节点事实，
		// 私有 context.local 不重复传输，避免两个来源长期漂移。
		build := buildinfo.Current()
		contextResult.Runtime = &capabilityRuntimeContext{
			Version: build.Version, BuildCommit: buildinfo.FullCommit(), BuildDate: build.BuildDate,
			OS: runtime.GOOS, Arch: runtime.GOARCH,
			AgentDockHome: r.cfg.AgentDockHome, AgentDockDefaultDir: r.cfg.AgentDockDefaultDir,
			DefaultCWD: r.ws.DefaultDisplay(), PathModel: config.PathModel,
		}
		contextResult.Tasks = &index
		if indexErr != nil {
			contextResult.Warnings = append(contextResult.Warnings, capabilityWarning{Source: "tasks", Message: "任务索引暂不可用；普通工具仍可使用。"})
		}
		if gitErr != nil {
			git.ErrorCode = "GIT_SNAPSHOT_FAILED"
		}
		if git.ErrorCode != "" {
			contextResult.Warnings = append(contextResult.Warnings, capabilityWarning{Source: "git", Message: git.ErrorCode})
		}
		if sessionErr != nil {
			contextResult.Warnings = append(contextResult.Warnings, capabilityWarning{Source: "sessions", Message: "命令完成事件计数暂不可用。"})
		}

	}
	if directoryErr != nil || pluginIndexErr != nil {
		contextResult.Warnings = append(contextResult.Warnings, capabilityWarning{Source: "plugins", Message: "插件目录暂不可用；当前工作区和内置工具仍可使用。"})
	}
	if skillErr != nil {
		contextResult.Warnings = append(contextResult.Warnings, capabilityWarning{Source: "skills", Message: "Skill 索引暂不可用。"})
	}
	if dynamicMCPErr != nil {
		contextResult.Warnings = append(contextResult.Warnings, capabilityWarning{Source: "dynamic_mcp", Message: "动态 MCP 索引暂不可用。"})
	}
	if commonSkillErr != nil {
		contextResult.Warnings = append(contextResult.Warnings, capabilityWarning{Source: "common_skills", Message: "通用 Skill 索引暂不可用；需要时可直接检查 ~/.agents/skills。"})
	}

	if requiresACP(r.cfg) {
		profiles := make([]capabilityACPProfileContext, 0, len(r.cfg.EffectiveACPProfiles()))
		for _, profile := range r.cfg.EffectiveACPProfiles() {
			profiles = append(profiles, capabilityACPProfileContext{ID: profile.ID, Kind: profile.Kind})
		}
		contextResult.ACP = &capabilityACPContext{
			Enabled:        true,
			DefaultProfile: r.cfg.EffectiveACPDefaultProfile(),
			Profiles:       profiles,
			Description: "本机 Coding Agent 通道（Agent Client Protocol）。仅当用户明确要求时使用，可用来获取独特见解与编排任务；" +
				"不是动态 MCP，不要用 mcp_tool_*。",
		}
	}

	if requiresNexus(r.cfg) && !nexusLocalOnly {
		contextResult.Rules = append(contextResult.Rules,
			"需要工作流模板时先调用 workflow_template_manage match，并使用返回的 source_template_ids；需要项目事实时调用 recall_search 定位，再用 recall_read 精确读取。",
		)
		templates, templateErr := r.templateCapabilityIndex(ctx)
		if templateErr != nil {
			contextResult.Warnings = append(contextResult.Warnings, capabilityWarning{Source: "workflow_templates", Message: "工作流模板索引暂不可用。"})
		}
		memoryItems, memoryErr := r.memoryCapabilityIndex(ctx)
		if memoryErr != nil {
			contextResult.Warnings = append(contextResult.Warnings, capabilityWarning{Source: "recall", Message: "记忆精简摘要暂不可用；需要项目事实时调用 recall_search/recall_read 精确确认。"})
		}
		contextResult.WorkflowTemplates = templates
		contextResult.Recall = &capabilityRecallContext{Enabled: true, Items: memoryItems}
	}
	if ctx.Err() == context.Canceled {
		return nil, ctx.Err()
	}

	serializeStarted := time.Now()
	var result Result
	if err := remarshal(contextResult, &result); err != nil {
		return nil, err
	}
	if !nexusLocalOnly && r.androidExecutor != nil {
		result["android_executor"] = r.androidExecutor.Status()
	}
	if !nexusLocalOnly {
		result["git"], result["running_sessions"], result["terminal_completions_pending"] = git, running, pending
		result["context_diagnostics"] = map[string]any{
			"complete": false, "binding_status": "pending",
			"components_complete": instructionSnapshotComplete(instructions) && directoryErr == nil && pluginIndexErr == nil && skillErr == nil && dynamicMCPErr == nil && commonSkillErr == nil && indexErr == nil,
			"rules_ms":            float64(ruleElapsed) / float64(time.Millisecond), "plugins_ms": float64(pluginElapsed) / float64(time.Millisecond),
			"skills_ms": float64(skillElapsed) / float64(time.Millisecond), "mcp_catalog_ms": float64(mcpElapsed) / float64(time.Millisecond),
			"serialization_ms": float64(time.Since(serializeStarted)) / float64(time.Millisecond), "context_ms": float64(time.Since(started)) / float64(time.Millisecond),
			"plugin_snapshot": pluginInfo, "skill_snapshot": skillInfo, "plugin_build": directory.Metrics,
			"plugin_revision":    directory.Revision,
			"snapshot_freshness": directory.SnapshotFreshness, "degraded_reason": directory.DegradedReason,
			"common_skills_ms": float64(commonElapsed) / float64(time.Millisecond), "common_snapshot": commonInfo,
			"task_index_ms": float64(taskElapsed) / float64(time.Millisecond),
			"git_ms":        float64(gitElapsed) / float64(time.Millisecond), "sessions_ms": float64(sessionElapsed) / float64(time.Millisecond),
		}
	}
	return result, nil
}

func (r *Runtime) agentDockContextTool(ctx context.Context, args map[string]any) (Result, error) {
	var request contextRequest
	if err := decodeToolInput("agentdock_context", args, &request); err != nil {
		return nil, err
	}
	return r.agentDockContext(ctx, false, request.Workdir)
}

type capabilityContext struct {
	Workspace          *workspace.Record                 `json:"workspace,omitempty"`
	Tasks              *taskstate.TaskIndex              `json:"tasks,omitempty"`
	InstructionFiles   *agentinstructions.Snapshot       `json:"instruction_files,omitempty"`
	Runtime            *capabilityRuntimeContext         `json:"runtime,omitempty"`
	BuiltinToolBundles []capabilityBuiltinToolBundleItem `json:"builtin_tool_bundles,omitempty"`
	Skills             []capabilitySkillItem             `json:"skills"`
	CommonSkills       *capabilityCommonSkillIndex       `json:"common_skills,omitempty"`
	Plugins            []capabilityPluginItem            `json:"plugins,omitempty"`
	DynamicMCP         []capabilityDynamicMCPItem        `json:"dynamic_mcp"`
	ACP                *capabilityACPContext             `json:"acp,omitempty"`
	WorkflowTemplates  []capabilityTemplateItem          `json:"workflow_templates"`
	Recall             *capabilityRecallContext          `json:"recall,omitempty"`
	Rules              []string                          `json:"rules"`
	Warnings           []capabilityWarning               `json:"warnings,omitempty"`
}

type capabilityRuntimeContext struct {
	Version             string `json:"version"`
	BuildCommit         string `json:"build_commit"`
	BuildDate           string `json:"build_date"`
	OS                  string `json:"os"`
	Arch                string `json:"arch"`
	AgentDockHome       string `json:"agentdock_home"`
	AgentDockDefaultDir string `json:"agentdock_default_dir"`
	DefaultCWD          string `json:"default_cwd"`
	PathModel           string `json:"path_model"`
}

type capabilitySkillItem struct {
	Name          string `json:"name"`
	Description   string `json:"description"`
	File          string `json:"file"`
	SkillRef      string `json:"skill_ref"`
	SourceType    string `json:"source_type"`
	SourceID      string `json:"source_id"`
	PluginName    string `json:"plugin_name,omitempty"`
	ContentDigest string `json:"content_digest,omitempty"`
}

type capabilityCommonSkillIndex struct {
	Root      string                      `json:"root"`
	Total     int                         `json:"total"`
	Truncated bool                        `json:"truncated"`
	Items     []capabilityCommonSkillItem `json:"items"`
}

type capabilityCommonSkillItem struct {
	Name          string `json:"name"`
	Description   string `json:"description"`
	File          string `json:"file"`
	SkillRef      string `json:"skill_ref"`
	SourceType    string `json:"source_type"`
	SourceID      string `json:"source_id"`
	ContentDigest string `json:"content_digest,omitempty"`
}

type capabilityDynamicMCPItem struct {
	Scope          string                  `json:"scope,omitempty"`
	Origin         string                  `json:"origin,omitempty"`
	DiscoverVia    string                  `json:"discover_via,omitempty"`
	InspectVia     string                  `json:"inspect_via,omitempty"`
	InvokeVia      string                  `json:"invoke_via,omitempty"`
	CatalogState   *mcpclient.CatalogState `json:"catalog_state,omitempty"`
	SourceType     string                  `json:"source_type,omitempty"`
	PluginName     string                  `json:"plugin_name,omitempty"`
	Revision       string                  `json:"revision,omitempty"`
	ServerVersion  string                  `json:"server_version,omitempty"`
	ToolCountKnown *bool                   `json:"tool_count_known,omitempty"`
	Name           string                  `json:"name"`
	Description    string                  `json:"description"`
	Status         string                  `json:"status"`
	ToolCount      int                     `json:"tool_count"`
	LastErrorCode  string                  `json:"last_error_code,omitempty"`
}

type capabilityPluginItem struct {
	Name           string `json:"name"`
	Description    string `json:"description"`
	SkillCount     int    `json:"skill_count"`
	MCPServerCount int    `json:"mcp_server_count"`
}

type capabilityACPContext struct {
	Enabled        bool                          `json:"enabled"`
	DefaultProfile string                        `json:"default_profile"`
	Profiles       []capabilityACPProfileContext `json:"profiles"`
	Description    string                        `json:"description"`
}

type capabilityACPProfileContext struct {
	ID   string `json:"id"`
	Kind string `json:"kind"`
}

type capabilityTemplateItem struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

type capabilityMemoryItem struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

type capabilityRecallContext struct {
	Enabled bool                   `json:"enabled"`
	Items   []capabilityMemoryItem `json:"items"`
}

type capabilityWarning struct {
	Source  string `json:"source"`
	Message string `json:"message"`
}

type capabilityTemplateList struct {
	Templates []capabilityTemplateListItem `json:"templates"`
}

type capabilityTemplateListItem struct {
	ID    string `json:"id"`
	Title string `json:"title"`
}

type capabilityRecallContextIndexResponse struct {
	ContextIndex capabilityRecallContextIndex `json:"context_index"`
}

type capabilityRecallContextIndex struct {
	Items     []capabilityRecallIndexItem `json:"items"`
	Truncated bool                        `json:"truncated"`
}

type capabilityRecallIndexItem struct {
	Kind     string   `json:"kind"`
	Path     string   `json:"path"`
	Title    string   `json:"title"`
	Summary  string   `json:"summary"`
	Keywords []string `json:"keywords"`
	Aliases  []string `json:"aliases"`
	Tags     []string `json:"tags"`
	CardType string   `json:"card_type"`
}

func (r *Runtime) dynamicMCPCapabilityIndex(includePluginMembers bool) ([]capabilityDynamicMCPItem, error) {
	ctx, cancel := context.WithTimeout(context.Background(), r.cfg.ContextBudget())
	defer cancel()
	directory, _, err := r.pluginStore.Snapshot(ctx)
	if err != nil {
		return nil, err
	}
	return r.dynamicMCPCapabilityIndexContext(ctx, includePluginMembers, directory)
}
func (r *Runtime) dynamicMCPCapabilityIndexContext(ctx context.Context, includePluginMembers bool, directory *pluginregistry.Directory) ([]capabilityDynamicMCPItem, error) {
	servers, err := r.dynamicMCP.CapabilityItems(ctx, directory)
	if err != nil {
		return []capabilityDynamicMCPItem{}, err
	}
	items := make([]capabilityDynamicMCPItem, 0, len(servers))
	for _, server := range servers {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if !includePluginMembers {
			membership, owned := directory.MCPMembership(server.Name)
			if owned && (membership.Heavy || capabilityrouting.RequiresExplicitPluginLoad(membership.Plugin)) {
				continue
			}
		}
		var known *bool
		revision, version := "", ""
		scope, origin, discoverVia, inspectVia, invokeVia := "", "", "", "", ""
		if !includePluginMembers {
			value := server.ToolCountKnown
			known = &value
			revision = server.Revision
			version = server.ServerVersion
			scope = toolmcp.ScopeAgentDockMCP
			origin = toolmcp.ScopeAgentDockMCP + ":" + server.Name
			discoverVia, inspectVia, invokeVia = toolmcp.ToolList, toolmcp.ToolInspect, toolmcp.ToolCall
		}
		var catalogState *mcpclient.CatalogState
		if !includePluginMembers {
			value := server.CatalogState
			catalogState = &value
		}
		items = append(items, capabilityDynamicMCPItem{
			Scope: scope, Origin: origin, DiscoverVia: discoverVia, InspectVia: inspectVia, InvokeVia: invokeVia,
			CatalogState: catalogState,
			Name:         server.Name,
			Revision:     revision, ServerVersion: version, ToolCountKnown: known,
			Description:   truncateString(capabilityrouting.ServerDescription(server.Plugin, server.Name, server.Description), 240),
			SourceType:    capabilitySourceType(server.Plugin),
			PluginName:    server.Plugin,
			Status:        server.Status,
			ToolCount:     server.ToolCount,
			LastErrorCode: server.LastErrorCode,
		})
	}
	return items, nil
}

func (r *Runtime) skillCapabilityIndex(includePluginMembers bool) ([]capabilitySkillItem, error) {
	ctx, cancel := context.WithTimeout(context.Background(), r.cfg.ContextBudget())
	defer cancel()
	directory, _, err := r.pluginStore.Snapshot(ctx)
	if err != nil {
		return []capabilitySkillItem{}, err
	}
	items, _, err := r.contextSkillIndex(ctx, directory, includePluginMembers)
	return items, err
}

func (r *Runtime) pluginCapabilityIndex() ([]capabilityPluginItem, error) {
	ctx, cancel := context.WithTimeout(context.Background(), r.cfg.ContextBudget())
	defer cancel()
	directory, _, err := r.pluginStore.Snapshot(ctx)
	if err != nil {
		return []capabilityPluginItem{}, err
	}
	return pluginCapabilityIndexFromDirectory(directory), nil
}

func pluginCapabilityIndexFromDirectory(directory *pluginregistry.Directory) []capabilityPluginItem {
	definitions := directory.Definitions()
	items := make([]capabilityPluginItem, 0, len(definitions))
	for _, definition := range definitions {
		if !definition.Enabled || (!definition.Heavy && !capabilityrouting.RequiresExplicitPluginLoad(definition.Name)) {
			continue
		}
		items = append(items, capabilityPluginItem{
			Name: definition.Name, Description: truncateString(capabilityrouting.PluginDescription(definition.Name, definition.Description), 320),
			SkillCount: len(definition.Skills), MCPServerCount: len(definition.MCPServers),
		})
	}
	return items
}

func hasExplicitLoadPlugin(directory *pluginregistry.Directory) bool {
	if directory == nil {
		return false
	}
	for _, definition := range directory.Definitions() {
		if definition.Enabled && capabilityrouting.RequiresExplicitPluginLoad(definition.Name) {
			return true
		}
	}
	return false
}

func (r *Runtime) templateCapabilityIndex(ctx context.Context) ([]capabilityTemplateItem, error) {
	result, err := r.taskTools.WorkflowManage(ctx, tooltask.WorkflowRequest{Action: "list", TemplateStatus: "active"})
	if err != nil {
		return []capabilityTemplateItem{}, err
	}
	var listed capabilityTemplateList
	if err := remarshal(result, &listed); err != nil {
		return []capabilityTemplateItem{}, err
	}
	items := make([]capabilityTemplateItem, 0, len(listed.Templates))
	for _, listedItem := range listed.Templates {
		name := strings.TrimSpace(listedItem.ID)
		if name == "" {
			continue
		}
		items = append(items, capabilityTemplateItem{Name: name, Description: truncateString(strings.TrimSpace(listedItem.Title), 160)})
	}
	sort.SliceStable(items, func(i, j int) bool { return items[i].Name < items[j].Name })
	return items, nil
}

func (r *Runtime) memoryCapabilityIndex(ctx context.Context) ([]capabilityMemoryItem, error) {
	ctx, cancel := context.WithTimeout(ctx, time.Duration(capMaxInt(1000, capMinInt(config.RecallTimeoutMS, 5000)))*time.Millisecond)
	defer cancel()
	result, err := r.recall.ContextIndex(ctx, 3000)
	if err != nil {
		return []capabilityMemoryItem{}, err
	}
	var response capabilityRecallContextIndexResponse
	if err := remarshal(result, &response); err != nil {
		return []capabilityMemoryItem{}, err
	}
	items := make([]capabilityMemoryItem, 0, len(response.ContextIndex.Items))
	seen := make(map[string]struct{}, len(response.ContextIndex.Items))
	for _, item := range response.ContextIndex.Items {
		path := strings.TrimSpace(item.Path)
		if path == "" {
			continue
		}
		if _, exists := seen[path]; exists {
			continue
		}
		seen[path] = struct{}{}
		items = append(items, capabilityMemoryItem{Name: path, Description: recallIndexDescription(item)})
	}
	return items, nil
}

func recallIndexDescription(item capabilityRecallIndexItem) string {
	if summary := strings.TrimSpace(item.Summary); summary != "" {
		if title := strings.TrimSpace(item.Title); title != "" {
			return truncateString(title+" — "+summary, 360)
		}
		return truncateString(summary, 360)
	}
	parts := []string{}
	if title := strings.TrimSpace(item.Title); title != "" {
		parts = append(parts, title)
	}
	if kind := strings.TrimSpace(item.Kind); kind != "" {
		parts = append(parts, kind)
	}
	if cardType := strings.TrimSpace(item.CardType); cardType != "" {
		parts = append(parts, cardType)
	}
	labels := append(append(append([]string{}, item.Keywords...), item.Aliases...), item.Tags...)
	if len(labels) > 0 {
		parts = append(parts, strings.Join(labels, ", "))
	}
	return truncateString(strings.Join(parts, " · "), 360)
}

func capMinInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func capMaxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func capabilitySourceType(plugin string) string {
	if plugin != "" {
		return "plugin"
	}
	return "standalone"
}

func instructionSnapshotComplete(value agentinstructions.Snapshot) bool {
	if !value.AutoLoad {
		return true
	}
	for _, file := range value.Files {
		if file.Status == "error" || file.Status == "skipped" {
			return false
		}
	}
	return true
}
