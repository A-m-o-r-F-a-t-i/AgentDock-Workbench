package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/uvwt/agentdock/internal/activity"
	"github.com/uvwt/agentdock/internal/agentinstructions"
	"github.com/uvwt/agentdock/internal/workspace"
)

type workspaceContextRequest = contextRequest

type workspaceContextResult struct {
	Workdir         string                   `json:"workdir"`
	WorkspaceRoot   string                   `json:"workspace_root"`
	Instructions    []agentinstructions.File `json:"instructions"`
	WorkspaceSkills []workspaceSkillItem     `json:"workspace_skills"`
	Warnings        []capabilityWarning      `json:"warnings"`
}

type workspaceSkillItem struct {
	Name          string `json:"name"`
	Description   string `json:"description"`
	File          string `json:"file"`
	SkillRef      string `json:"skill_ref"`
	SourceType    string `json:"source_type"`
	SourceID      string `json:"source_id"`
	ContentDigest string `json:"content_digest,omitempty"`
}

// workspaceContext 复用当前作用域规则快照，并可取消地读取工作区本地 Skill 索引。
// workdir 只用于本次选择，不修改 Workspace 默认 cwd，也不保存为 Runtime 状态。
func (r *Runtime) workspaceContext(ctx context.Context, workdir string) (Result, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if strings.TrimSpace(workdir) == "" {
		binding := activity.FromContext(ctx)
		if binding.WorkspaceID != "" {
			record, err := r.workspaceRegistry.Select(ctx, binding.WorkspaceID, "")
			if err != nil {
				return nil, workspaceFailure(err, binding, nil)
			}
			if record.Runtime != workspace.HostRuntime() {
				return nil, toolError("CONTEXT_WORKSPACE_RUNTIME", "The bound workspace requires its own runtime for instruction loading.", "runtime")
			}
			workdir = filepath.Join(record.Root, record.DefaultWorkdir)
		}
	}
	resolved, err := r.ws.ResolveExisting(workdir)
	if err != nil {
		return nil, toolErrorDetails("INVALID_ARGUMENT", "workspace workdir must resolve to an existing host directory", "validation", map[string]any{"workdir": workdir})
	}
	info, err := os.Stat(resolved.Abs)
	if err != nil || !info.IsDir() {
		return nil, toolErrorDetails("INVALID_ARGUMENT", "workspace workdir must be a directory", "validation", map[string]any{"workdir": workdir})
	}

	instructions, err := r.InstructionFiles(ctx, resolved.Abs)
	if err != nil {
		return nil, err
	}

	warnings := []capabilityWarning{}
	workspaceSkills := []workspaceSkillItem{}
	skillIndex, skillErr := scanWorkspaceFilesystemSkillsContext(ctx, instructions.WorkspaceRoot)
	if skillErr != nil {
		warnings = append(warnings, capabilityWarning{Source: "workspace_skills", Message: "工作区 Skill 索引暂不可用。"})
	} else {
		workspaceSkills = make([]workspaceSkillItem, 0, len(skillIndex.Items))
		for _, item := range skillIndex.Items {
			skillRef, sourceID, refErr := r.skills.WorkspaceSkillRef(instructions.WorkspaceRoot, item.Name)
			if refErr != nil {
				warnings = append(warnings, capabilityWarning{Source: "workspace_skills", Message: "工作区 Skill 引用生成失败：" + item.Name})
				continue
			}
			workspaceSkills = append(workspaceSkills, workspaceSkillItem{
				Name: item.Name, Description: item.Description,
				File: skillRef + "/SKILL.md", SkillRef: skillRef,
				SourceType: "workspace", SourceID: sourceID,
			})
		}
		if skillIndex.Truncated {
			warnings = append(warnings, capabilityWarning{Source: "workspace_skills", Message: "工作区 Skill 数量超过索引上限，仅返回稳定排序后的前 50 项。"})
		}
	}

	value := workspaceContextResult{
		Workdir:         instructions.Workdir,
		WorkspaceRoot:   instructions.WorkspaceRoot,
		Instructions:    instructions.Files,
		WorkspaceSkills: workspaceSkills,
		Warnings:        warnings,
	}
	var result Result
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := remarshal(value, &result); err != nil {
		return nil, err
	}
	complete := instructionSnapshotComplete(instructions) && len(warnings) == 0
	result["context_diagnostics"] = map[string]any{
		"complete": complete, "components_complete": complete, "binding_status": "request_local",
	}
	if err := attachContextRevision(result); err != nil {
		return nil, err
	}
	return result, nil
}

func (r *Runtime) workspaceContextTool(ctx context.Context, args map[string]any) (Result, error) {
	var request workspaceContextRequest
	if err := decodeToolInput("workspace_context", args, &request); err != nil {
		return nil, err
	}
	return r.workspaceContext(ctx, request.Workdir)
}
