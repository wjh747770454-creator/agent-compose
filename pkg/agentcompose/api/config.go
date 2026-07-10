package api

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/emptypb"

	appconfig "agent-compose/pkg/config"
	domain "agent-compose/pkg/model"
	"agent-compose/pkg/workspaces"
	agentcomposev1 "agent-compose/proto/agentcompose/v1"
)

type ConfigStore interface {
	ListGlobalEnv(ctx context.Context) ([]domain.SandboxEnvVar, error)
	ReplaceGlobalEnv(ctx context.Context, items []domain.SandboxEnvVar) ([]domain.SandboxEnvVar, error)
	ListWorkspaceConfigs(ctx context.Context) ([]domain.WorkspaceConfig, error)
	GetWorkspaceConfig(ctx context.Context, id string) (domain.WorkspaceConfig, error)
	CreateWorkspaceConfig(ctx context.Context, item domain.WorkspaceConfig) (domain.WorkspaceConfig, error)
	UpdateWorkspaceConfig(ctx context.Context, item domain.WorkspaceConfig) (domain.WorkspaceConfig, error)
	DeleteWorkspaceConfig(ctx context.Context, id string) error
	GetCapabilityGateway(ctx context.Context) (domain.CapabilityGatewaySettings, error)
	SaveCapabilityGateway(ctx context.Context, settings domain.CapabilityGatewaySettings) (domain.CapabilityGatewaySettings, error)
}

type ConfigHandler struct {
	config *appconfig.Config
	store  ConfigStore
}

func NewConfigHandler(config *appconfig.Config, store ConfigStore) *ConfigHandler {
	return &ConfigHandler{config: config, store: store}
}

func (h *ConfigHandler) GetRuntimeConfig(ctx context.Context, req *connect.Request[emptypb.Empty]) (*connect.Response[agentcomposev1.RuntimeConfigResponse], error) {
	_ = ctx
	_ = req
	return connect.NewResponse(&agentcomposev1.RuntimeConfigResponse{
		AgentComposeHost: strings.TrimRight(strings.TrimSpace(h.config.AgentComposeHost), "/"),
	}), nil
}

func (h *ConfigHandler) GetGlobalEnvConfig(ctx context.Context, req *connect.Request[emptypb.Empty]) (*connect.Response[agentcomposev1.GlobalEnvConfigResponse], error) {
	_ = req
	items, err := h.store.ListGlobalEnv(ctx)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(GlobalEnvConfigToProto(items)), nil
}

func (h *ConfigHandler) UpdateGlobalEnvConfig(ctx context.Context, req *connect.Request[agentcomposev1.UpdateGlobalEnvConfigRequest]) (*connect.Response[agentcomposev1.GlobalEnvConfigResponse], error) {
	items := domain.NormalizeEnvItems(EnvItemsFromProto(req.Msg.GetEnvItems()))
	items, err := h.preserveUnchangedGlobalEnvSecrets(ctx, items)
	if err != nil {
		return nil, err
	}
	saved, err := h.store.ReplaceGlobalEnv(ctx, items)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(GlobalEnvConfigToProto(saved)), nil
}

func (h *ConfigHandler) GetCapabilityGatewayConfig(ctx context.Context, req *connect.Request[emptypb.Empty]) (*connect.Response[agentcomposev1.CapabilityGatewayConfig], error) {
	_ = req
	settings, err := h.store.GetCapabilityGateway(ctx)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(CapabilityGatewayConfigToProto(settings)), nil
}

func (h *ConfigHandler) UpdateCapabilityGatewayConfig(ctx context.Context, req *connect.Request[agentcomposev1.UpdateCapabilityGatewayConfigRequest]) (*connect.Response[agentcomposev1.CapabilityGatewayConfig], error) {
	token := strings.TrimSpace(req.Msg.GetToken())
	saved, err := h.store.SaveCapabilityGateway(ctx, domain.CapabilityGatewaySettings{
		Addr:  req.Msg.GetAddr(),
		Token: token,
	})
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(CapabilityGatewayConfigToProto(saved)), nil
}

func (h *ConfigHandler) preserveUnchangedGlobalEnvSecrets(ctx context.Context, items []domain.SandboxEnvVar) ([]domain.SandboxEnvVar, error) {
	existingItems, err := h.store.ListGlobalEnv(ctx)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	existingByName := make(map[string]domain.SandboxEnvVar, len(existingItems))
	for _, item := range existingItems {
		name := strings.TrimSpace(item.Name)
		if name != "" {
			existingByName[name] = item
		}
	}
	for index, item := range items {
		name := strings.TrimSpace(item.Name)
		if name == "" || !item.Secret || strings.TrimSpace(item.Value) != "" {
			continue
		}
		existing, ok := existingByName[name]
		if !ok || !existing.Secret || existing.Value == "" {
			return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("secret env %s requires a value", name))
		}
		items[index].Value = existing.Value
	}
	return items, nil
}

func (h *ConfigHandler) ListWorkspaceConfigs(ctx context.Context, req *connect.Request[emptypb.Empty]) (*connect.Response[agentcomposev1.ListWorkspaceConfigsResponse], error) {
	_ = req
	items, err := h.store.ListWorkspaceConfigs(ctx)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	resp := &agentcomposev1.ListWorkspaceConfigsResponse{}
	for _, item := range items {
		resp.Workspaces = append(resp.Workspaces, WorkspaceConfigToProto(item))
	}
	return connect.NewResponse(resp), nil
}

func (h *ConfigHandler) CreateWorkspaceConfig(ctx context.Context, req *connect.Request[agentcomposev1.CreateWorkspaceConfigRequest]) (*connect.Response[agentcomposev1.WorkspaceConfigResponse], error) {
	configJSON := strings.TrimSpace(req.Msg.GetConfigJson())
	workspaceType := strings.ToLower(strings.TrimSpace(req.Msg.GetType()))
	workspaceID := ""
	if workspaceType == "file" {
		workspaceID = uuid.NewString()
		configJSON = workspaces.DefaultFileConfigJSON(h.config, workspaceID)
		if _, err := workspaces.ValidateFileWorkspaceConfig(h.config, workspaceID, configJSON); err != nil {
			return nil, connect.NewError(connect.CodeInvalidArgument, err)
		}
		if err := h.checkFileWorkspaceContentCreatable(workspaceID); err != nil {
			return nil, connect.NewError(connect.CodeInternal, err)
		}
	}
	item, err := h.store.CreateWorkspaceConfig(ctx, domain.WorkspaceConfig{
		ID:         workspaceID,
		Name:       req.Msg.GetName(),
		Type:       workspaceType,
		ConfigJSON: configJSON,
		Comment:    req.Msg.GetComment(),
	})
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	if workspaceType == "file" {
		if err := h.createFileWorkspaceContent(item.ID, item.ConfigJSON); err != nil {
			deleteErr := h.store.DeleteWorkspaceConfig(ctx, item.ID)
			if deleteErr != nil {
				return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("create file workspace content: %w; rollback workspace config: %v", err, deleteErr))
			}
			return nil, connect.NewError(connect.CodeInternal, err)
		}
	}
	return connect.NewResponse(&agentcomposev1.WorkspaceConfigResponse{Workspace: WorkspaceConfigToProto(item)}), nil
}

func (h *ConfigHandler) UpdateWorkspaceConfig(ctx context.Context, req *connect.Request[agentcomposev1.UpdateWorkspaceConfigRequest]) (*connect.Response[agentcomposev1.WorkspaceConfigResponse], error) {
	configJSON := strings.TrimSpace(req.Msg.GetConfigJson())
	workspaceType := strings.ToLower(strings.TrimSpace(req.Msg.GetType()))
	previous, err := h.store.GetWorkspaceConfig(ctx, req.Msg.GetWorkspaceId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	if workspaceType == "file" {
		configJSON = workspaces.DefaultFileConfigJSON(h.config, req.Msg.GetWorkspaceId())
		if _, err := workspaces.ValidateFileWorkspaceConfig(h.config, req.Msg.GetWorkspaceId(), configJSON); err != nil {
			return nil, connect.NewError(connect.CodeInvalidArgument, err)
		}
	}
	wasFile := strings.EqualFold(strings.TrimSpace(previous.Type), "file")
	if workspaceType == "file" {
		if err := h.checkFileWorkspaceContentCreatable(req.Msg.GetWorkspaceId()); err != nil {
			return nil, connect.NewError(connect.CodeInternal, err)
		}
	} else if wasFile {
		if err := h.checkFileWorkspaceContentRemovable(previous); err != nil {
			return nil, connect.NewError(connect.CodeInternal, err)
		}
	}
	item, err := h.store.UpdateWorkspaceConfig(ctx, domain.WorkspaceConfig{
		ID:         req.Msg.GetWorkspaceId(),
		Name:       req.Msg.GetName(),
		Type:       workspaceType,
		ConfigJSON: configJSON,
		Comment:    req.Msg.GetComment(),
	})
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	if workspaceType == "file" {
		if err := h.createFileWorkspaceContent(item.ID, item.ConfigJSON); err != nil {
			_, rollbackErr := h.store.UpdateWorkspaceConfig(ctx, previous)
			if rollbackErr != nil {
				return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("create file workspace content: %w; rollback workspace config: %v", err, rollbackErr))
			}
			return nil, connect.NewError(connect.CodeInternal, err)
		}
	} else if wasFile {
		if err := h.removeFileWorkspaceContent(previous); err != nil {
			_, rollbackErr := h.store.UpdateWorkspaceConfig(ctx, previous)
			if rollbackErr != nil {
				return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("remove file workspace content: %w; rollback workspace config: %v", err, rollbackErr))
			}
			return nil, connect.NewError(connect.CodeInternal, err)
		}
	}
	return connect.NewResponse(&agentcomposev1.WorkspaceConfigResponse{Workspace: WorkspaceConfigToProto(item)}), nil
}

func (h *ConfigHandler) DeleteWorkspaceConfig(ctx context.Context, req *connect.Request[agentcomposev1.WorkspaceConfigIDRequest]) (*connect.Response[emptypb.Empty], error) {
	workspace, err := h.store.GetWorkspaceConfig(ctx, req.Msg.GetWorkspaceId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	if strings.EqualFold(strings.TrimSpace(workspace.Type), "file") {
		if err := h.checkFileWorkspaceContentRemovable(workspace); err != nil {
			return nil, connect.NewError(connect.CodeInternal, err)
		}
	}
	if err := h.store.DeleteWorkspaceConfig(ctx, req.Msg.GetWorkspaceId()); err != nil {
		if errors.Is(err, domain.ErrReferenced) {
			return nil, connect.NewError(connect.CodeFailedPrecondition, err)
		}
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	if strings.EqualFold(strings.TrimSpace(workspace.Type), "file") {
		if err := h.removeFileWorkspaceContent(workspace); err != nil {
			_, rollbackErr := h.store.CreateWorkspaceConfig(ctx, workspace)
			if rollbackErr != nil {
				return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("remove file workspace content: %w; rollback workspace config: %v", err, rollbackErr))
			}
			return nil, connect.NewError(connect.CodeInternal, err)
		}
	}
	return connect.NewResponse(&emptypb.Empty{}), nil
}

func (h *ConfigHandler) createFileWorkspaceContent(workspaceID, configJSON string) error {
	content, err := workspaces.OpenFileWorkspaceContent(h.config, domain.WorkspaceConfig{
		ID:         workspaceID,
		Type:       "file",
		ConfigJSON: configJSON,
	})
	if err != nil {
		return err
	}
	return content.Root.Close()
}

func (h *ConfigHandler) checkFileWorkspaceContentCreatable(workspaceID string) error {
	relRoot, err := workspaces.FileWorkspaceContentRelRoot(workspaceID)
	if err != nil {
		return err
	}
	dataRoot, err := workspaces.OpenFileWorkspaceDataRoot(h.config)
	if err != nil {
		return err
	}
	defer func() { _ = dataRoot.Close() }()
	for _, dir := range []string{"workspaces", filepath.ToSlash(filepath.Join("workspaces", strings.TrimSpace(workspaceID))), relRoot} {
		info, err := dataRoot.Lstat(dir)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("file workspace path %s is a symlink", dir)
		}
		if !info.IsDir() {
			return fmt.Errorf("file workspace path %s is not a directory", dir)
		}
	}
	return nil
}

func (h *ConfigHandler) checkFileWorkspaceContentRemovable(workspace domain.WorkspaceConfig) error {
	dataRoot, _, err := h.fileWorkspaceContentRemovalTarget(workspace)
	if err != nil {
		return err
	}
	return dataRoot.Close()
}

func (h *ConfigHandler) removeFileWorkspaceContent(workspace domain.WorkspaceConfig) error {
	dataRoot, relRoot, err := h.fileWorkspaceContentRemovalTarget(workspace)
	if err != nil {
		return err
	}
	defer func() { _ = dataRoot.Close() }()
	return dataRoot.RemoveAll(relRoot)
}

func (h *ConfigHandler) fileWorkspaceContentRemovalTarget(workspace domain.WorkspaceConfig) (*os.Root, string, error) {
	dataRoot, err := workspaces.OpenFileWorkspaceDataRoot(h.config)
	if err != nil {
		return nil, "", err
	}
	relRoot, err := workspaces.FileWorkspaceContentRelRoot(workspace.ID)
	if err != nil {
		_ = dataRoot.Close()
		return nil, "", err
	}
	info, err := dataRoot.Lstat(relRoot)
	if err != nil && !os.IsNotExist(err) {
		_ = dataRoot.Close()
		return nil, "", err
	}
	if err == nil && info.Mode()&os.ModeSymlink != 0 {
		_ = dataRoot.Close()
		return nil, "", fmt.Errorf("file workspace content root %s is a symlink", relRoot)
	}
	return dataRoot, relRoot, nil
}
