package api

import (
	"context"
	"errors"
	"strings"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/emptypb"

	"agent-compose/pkg/capabilities"
	"agent-compose/pkg/capability"
	domain "agent-compose/pkg/model"
	agentcomposev1 "agent-compose/proto/agentcompose/v1"
)

type CapabilityGatewayStore interface {
	GetCapabilityGateway(ctx context.Context) (domain.CapabilityGatewaySettings, error)
	SaveCapabilityGateway(ctx context.Context, settings domain.CapabilityGatewaySettings) (domain.CapabilityGatewaySettings, error)
}

type CapabilityRuntimeConfig interface {
	CapProxyListen() string
}

type CapabilityHandler struct {
	provider capabilities.Provider
	store    CapabilityGatewayStore
	runtime  CapabilityRuntimeConfig
}

func NewCapabilityHandler(provider capabilities.Provider, store CapabilityGatewayStore, runtime CapabilityRuntimeConfig) *CapabilityHandler {
	return &CapabilityHandler{provider: provider, store: store, runtime: runtime}
}

func (h *CapabilityHandler) GetCapabilityStatus(ctx context.Context, req *connect.Request[agentcomposev1.GetCapabilityStatusRequest]) (*connect.Response[agentcomposev1.CapabilityStatusResponse], error) {
	_ = req
	status := h.provider.Status(ctx)
	proxyListenConfigured := h.runtime != nil && strings.TrimSpace(h.runtime.CapProxyListen()) != ""
	proxyTargetConfigured := strings.TrimSpace(h.provider.ProxyTarget()) != ""
	return connect.NewResponse(&agentcomposev1.CapabilityStatusResponse{
		Configured:            status.Configured,
		Ok:                    status.OK,
		Status:                status.Status,
		ServiceCount:          status.ServiceCount,
		Error:                 status.Error,
		RuntimeConfigured:     proxyListenConfigured && proxyTargetConfigured,
		ProxyListenConfigured: proxyListenConfigured,
		ProxyTargetConfigured: proxyTargetConfigured,
	}), nil
}

func (h *CapabilityHandler) ListCapabilitySets(ctx context.Context, req *connect.Request[agentcomposev1.ListCapabilitySetsRequest]) (*connect.Response[agentcomposev1.ListCapabilitySetsResponse], error) {
	_ = req
	capsets, err := h.provider.ListCapsets(ctx)
	if err != nil {
		return nil, connect.NewError(connect.CodeUnavailable, err)
	}
	resp := &agentcomposev1.ListCapabilitySetsResponse{}
	for _, item := range capsets {
		if !item.Enabled {
			continue
		}
		resp.Capsets = append(resp.Capsets, &agentcomposev1.CapabilitySet{
			Id:          item.ID,
			Name:        item.Name,
			Description: item.Description,
			Enabled:     item.Enabled,
		})
	}
	return connect.NewResponse(resp), nil
}

func (h *CapabilityHandler) GetCapabilityCatalog(ctx context.Context, req *connect.Request[agentcomposev1.GetCapabilityCatalogRequest]) (*connect.Response[agentcomposev1.GetCapabilityCatalogResponse], error) {
	catalog, err := h.provider.Catalog(ctx, req.Msg.GetCapsetId())
	if err != nil {
		return nil, CapabilityConnectError(err)
	}
	return connect.NewResponse(CapabilityCatalogToProto(catalog)), nil
}

func (h *CapabilityHandler) GetCapabilityGatewayConfig(ctx context.Context, req *connect.Request[emptypb.Empty]) (*connect.Response[agentcomposev1.CapabilityGatewayConfig], error) {
	_ = req
	settings, err := h.store.GetCapabilityGateway(ctx)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(CapabilityGatewayConfigToProto(settings)), nil
}

func (h *CapabilityHandler) UpdateCapabilityGatewayConfig(ctx context.Context, req *connect.Request[agentcomposev1.UpdateCapabilityGatewayConfigRequest]) (*connect.Response[agentcomposev1.CapabilityGatewayConfig], error) {
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

func CapabilityConnectError(err error) error {
	switch {
	case errors.Is(err, capability.ErrNotConfigured):
		return connect.NewError(connect.CodeFailedPrecondition, err)
	case errors.Is(err, capability.ErrInvalidCatalog):
		return connect.NewError(connect.CodeInvalidArgument, err)
	default:
		return connect.NewError(connect.CodeUnavailable, err)
	}
}

func CapabilityGatewayConfigToProto(settings domain.CapabilityGatewaySettings) *agentcomposev1.CapabilityGatewayConfig {
	return &agentcomposev1.CapabilityGatewayConfig{
		Addr:     settings.Addr,
		TokenSet: strings.TrimSpace(settings.Token) != "",
	}
}

func CapabilityCatalogToProto(item capability.Catalog) *agentcomposev1.GetCapabilityCatalogResponse {
	resp := &agentcomposev1.GetCapabilityCatalogResponse{
		CapsetId:    item.CapsetID,
		Name:        item.Name,
		Description: item.Description,
	}
	for _, method := range item.Methods {
		resp.Methods = append(resp.Methods, CapabilityMethodToProto(method))
	}
	return resp
}

func CapabilityMethodToProto(item capability.Method) *agentcomposev1.CapabilityMethod {
	resp := &agentcomposev1.CapabilityMethod{
		ServiceId:               item.ServiceID,
		InstanceId:              item.InstanceID,
		RuntimeMode:             item.RuntimeMode,
		MethodFullName:          item.MethodFullName,
		RequestMessageFullName:  item.RequestMessageFullName,
		ResponseMessageFullName: item.ResponseMessageFullName,
		BackendInstanceStatus:   item.BackendInstanceStatus,
	}
	for _, endpoint := range item.Endpoints {
		resp.Endpoints = append(resp.Endpoints, &agentcomposev1.CapabilityEndpoint{
			Protocol:     endpoint.Protocol,
			Endpoint:     endpoint.Endpoint,
			MethodPath:   endpoint.MethodPath,
			Metadata:     endpoint.Metadata,
			ToolName:     endpoint.ToolName,
			Procedure:    endpoint.Procedure,
			HttpMethod:   endpoint.HTTPMethod,
			ContentTypes: endpoint.ContentTypes,
		})
	}
	return resp
}
