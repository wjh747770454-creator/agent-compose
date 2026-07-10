package runs

import (
	"errors"
	"testing"

	driverpkg "agent-compose/pkg/driver"
	agentcomposev2 "agent-compose/proto/agentcompose/v2"
)

func TestAttachInputPumpsCloseRuntimeInputOnReceiveError(t *testing.T) {
	receiveErr := errors.New("stream reset")

	t.Run("command", func(t *testing.T) {
		interaction := &closingRuntimeInteraction{}
		pumpRunAttachInput(func() (*agentcomposev2.RunAttachRequest, error) {
			return nil, receiveErr
		}, interaction)
		if interaction.closeCalls != 1 {
			t.Fatalf("CloseSend calls = %d, want 1", interaction.closeCalls)
		}
	})

	t.Run("prompt", func(t *testing.T) {
		interaction := &closingRuntimeInteraction{}
		input := &promptWrapperInput{interaction: interaction}
		pumpRunPromptAttachInput(func() (*agentcomposev2.RunAttachRequest, error) {
			return nil, receiveErr
		}, input, nil)
		if interaction.closeCalls != 1 {
			t.Fatalf("CloseSend calls = %d, want 1", interaction.closeCalls)
		}
		if len(interaction.sent) != 1 || string(interaction.sent[0].Data) != "{\"seq\":0,\"type\":\"eof\",\"v\":1}\n" {
			t.Fatalf("sent frames = %#v, want prompt EOF", interaction.sent)
		}
	})
}

type closingRuntimeInteraction struct {
	sent       []driverpkg.RuntimeInputFrame
	closeCalls int
}

func (i *closingRuntimeInteraction) Send(frame driverpkg.RuntimeInputFrame) error {
	i.sent = append(i.sent, frame)
	return nil
}

func (i *closingRuntimeInteraction) CloseSend() error {
	i.closeCalls++
	return nil
}

func (*closingRuntimeInteraction) Recv() (driverpkg.RuntimeOutputFrame, error) {
	return driverpkg.RuntimeOutputFrame{}, errors.New("unused")
}

func (*closingRuntimeInteraction) Wait() (driverpkg.RuntimeResult, error) {
	return driverpkg.RuntimeResult{}, errors.New("unused")
}
