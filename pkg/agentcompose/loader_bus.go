package agentcompose

import (
	"agent-compose/pkg/agentcompose/loaders"

	"github.com/samber/do/v2"
)

type LoaderBus = loaders.Bus

func NewLoaderBus(di do.Injector) (*LoaderBus, error) {
	return loaders.NewBus(di)
}
