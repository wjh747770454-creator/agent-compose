package agentcompose

import (
	"context"
	"errors"
	"strings"

	"connectrpc.com/connect"
	"github.com/samber/do/v2"
	"google.golang.org/protobuf/types/known/emptypb"

	"agent-compose/pkg/agentcompose/api"
	"agent-compose/pkg/agentcompose/capabilities"
	"agent-compose/pkg/capability"
	appconfig "agent-compose/pkg/config"
	agentcomposev1 "agent-compose/proto/agentcompose/v1"
)

type CapabilityProvider = capabilities.Provider

type capabilityIntegration interface{ CapabilityProvider }

func NewCapabilityProvider(di do.Injector) (capabilityIntegration, error) {
	conf := do.MustInvoke[*appconfig.Config](di)
	return capabilities.NewDynamicProvider(do.MustInvoke[*ConfigStore](di), conf.CapGRPCTarget), nil
}

func (s *Service) GetCapabilityStatus(ctx context.Context, req *connect.Request[agentcomposev1.GetCapabilityStatusRequest]) (*connect.Response[agentcomposev1.CapabilityStatusResponse], error) {
	_ = req
	status := s.cap.Status(ctx)
	proxyListenConfigured := s.config != nil && strings.TrimSpace(s.config.CapGRPCListen) != ""
	proxyTargetConfigured := strings.TrimSpace(s.cap.ProxyTarget()) != ""
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

func (s *Service) ListCapabilitySets(ctx context.Context, req *connect.Request[agentcomposev1.ListCapabilitySetsRequest]) (*connect.Response[agentcomposev1.ListCapabilitySetsResponse], error) {
	_ = req
	capsets, err := s.cap.ListCapsets(ctx)
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

func (s *Service) GetCapabilityCatalog(ctx context.Context, req *connect.Request[agentcomposev1.GetCapabilityCatalogRequest]) (*connect.Response[agentcomposev1.GetCapabilityCatalogResponse], error) {
	catalog, err := s.cap.Catalog(ctx, req.Msg.GetCapsetId())
	if err != nil {
		return nil, capabilityConnectError(err)
	}
	return connect.NewResponse(toProtoCapabilityCatalog(catalog)), nil
}

func (s *Service) GetCapabilityGatewayConfig(ctx context.Context, req *connect.Request[emptypb.Empty]) (*connect.Response[agentcomposev1.CapabilityGatewayConfig], error) {
	_ = req
	settings, err := s.configDB.GetCapabilityGateway(ctx)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(toProtoCapabilityGatewayConfig(settings)), nil
}

func (s *Service) UpdateCapabilityGatewayConfig(ctx context.Context, req *connect.Request[agentcomposev1.UpdateCapabilityGatewayConfigRequest]) (*connect.Response[agentcomposev1.CapabilityGatewayConfig], error) {
	token := strings.TrimSpace(req.Msg.GetToken())
	saved, err := s.configDB.SaveCapabilityGateway(ctx, CapabilityGatewaySettings{
		Addr:  req.Msg.GetAddr(),
		Token: token,
	})
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(toProtoCapabilityGatewayConfig(saved)), nil
}

func toProtoCapabilityGatewayConfig(settings CapabilityGatewaySettings) *agentcomposev1.CapabilityGatewayConfig {
	return api.CapabilityGatewayConfigToProto(settings)
}

func capabilityConnectError(err error) error {
	switch {
	case errors.Is(err, capability.ErrNotConfigured):
		return connect.NewError(connect.CodeFailedPrecondition, err)
	case errors.Is(err, capability.ErrInvalidCatalog):
		return connect.NewError(connect.CodeInvalidArgument, err)
	default:
		return connect.NewError(connect.CodeUnavailable, err)
	}
}

func toProtoCapabilityCatalog(item capability.Catalog) *agentcomposev1.GetCapabilityCatalogResponse {
	return api.CapabilityCatalogToProto(item)
}
