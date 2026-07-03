package adapters

import (
	"context"
	"fmt"
	"strings"

	"agent-compose/pkg/capabilities"
	"agent-compose/pkg/capproxy"
	domain "agent-compose/pkg/model"
)

type CapabilitySessionStore interface {
	ListSessions(context.Context, domain.SessionListOptions) (domain.SessionListResult, error)
}

type CapabilitySessionResolver struct {
	store CapabilitySessionStore
}

func NewCapabilitySessionResolver(store CapabilitySessionStore) *CapabilitySessionResolver {
	return &CapabilitySessionResolver{store: store}
}

func (r *CapabilitySessionResolver) ResolveCapabilitySession(ctx context.Context, token string) (capproxy.SessionBinding, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return capproxy.SessionBinding{}, fmt.Errorf("capability session token is required")
	}
	if r == nil || r.store == nil {
		return capproxy.SessionBinding{}, fmt.Errorf("capability session store is required")
	}
	offset := 0
	const pageSize = 200
	for {
		result, err := r.store.ListSessions(ctx, domain.SessionListOptions{Offset: offset, Limit: pageSize})
		if err != nil {
			return capproxy.SessionBinding{}, err
		}
		for _, session := range result.Sessions {
			if session == nil || capabilities.SessionToken(session) != token {
				continue
			}
			if session.Summary.VMStatus != domain.VMStatusRunning {
				return capproxy.SessionBinding{}, fmt.Errorf("capability session token is not active")
			}
			capsetIDs := capabilities.SessionCapsets(session)
			if len(capsetIDs) == 0 {
				return capproxy.SessionBinding{}, fmt.Errorf("session %s has no capability capset", session.Summary.ID)
			}
			return capproxy.SessionBinding{SessionID: session.Summary.ID, CapsetIDs: capsetIDs}, nil
		}
		if !result.HasMore {
			break
		}
		offset = result.NextOffset
	}
	return capproxy.SessionBinding{}, domain.ClassifyError(domain.ErrNotFound, "capability session token not found", nil)
}
