package agentcompose

import "agent-compose/pkg/agentcompose/domain"

var (
	ErrNotFound           = domain.ErrNotFound
	ErrInvalidArgument    = domain.ErrInvalidArgument
	ErrRequired           = domain.ErrRequired
	ErrAmbiguous          = domain.ErrAmbiguous
	ErrConflict           = domain.ErrConflict
	ErrAlreadyExists      = domain.ErrAlreadyExists
	ErrReferenced         = domain.ErrReferenced
	ErrFailedPrecondition = domain.ErrFailedPrecondition
	ErrBodyTooLarge       = domain.ErrBodyTooLarge
)

type classifiedError = domain.ClassifiedError

func classifyError(kind error, reason string, cause error) error {
	return domain.ClassifyError(kind, reason, cause)
}

func resourceError(kind error, resource, id, reason string, cause error) error {
	return domain.ResourceError(kind, resource, id, reason, cause)
}
