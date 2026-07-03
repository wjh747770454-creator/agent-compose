package api

import (
	"context"
	"fmt"
	"strings"

	"connectrpc.com/connect"

	"agent-compose/pkg/images"
	agentcomposev2 "agent-compose/proto/agentcompose/v2"
)

type ImageBackendSelector interface {
	ImageBackendForStore(agentcomposev2.ImageStoreKind) (images.Backend, error)
}

type ImageHandler struct {
	backends ImageBackendSelector
}

func NewImageHandler(backends ImageBackendSelector) *ImageHandler {
	return &ImageHandler{backends: backends}
}

func (h *ImageHandler) ListImages(ctx context.Context, req *connect.Request[agentcomposev2.ListImagesRequest]) (*connect.Response[agentcomposev2.ListImagesResponse], error) {
	backend, err := h.backends.ImageBackendForStore(req.Msg.GetStore())
	if err != nil {
		return nil, err
	}
	result, err := backend.ListImages(ctx, images.ListRequest{
		Query: strings.TrimSpace(req.Msg.GetQuery()),
		All:   req.Msg.GetAll(),
	})
	if err != nil {
		return nil, ConnectErrorForImageBackend("list images", "", err)
	}
	images, hasMore, nextOffset := images.PaginateProtoImages(result.Images, req.Msg.GetOffset(), req.Msg.GetLimit())
	return connect.NewResponse(&agentcomposev2.ListImagesResponse{
		Images:      images,
		TotalCount:  uint32(len(result.Images)),
		HasMore:     hasMore,
		NextOffset:  nextOffset,
		StoreStatus: result.StoreStatus,
	}), nil
}

func (h *ImageHandler) PullImage(ctx context.Context, req *connect.Request[agentcomposev2.PullImageRequest]) (*connect.Response[agentcomposev2.PullImageResponse], error) {
	imageRef := strings.TrimSpace(req.Msg.GetImageRef())
	if imageRef == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("image_ref is required"))
	}
	backend, err := h.backends.ImageBackendForStore(req.Msg.GetStore())
	if err != nil {
		return nil, err
	}
	result, err := backend.PullImage(ctx, images.PullRequest{
		ImageRef: imageRef,
		Platform: req.Msg.GetPlatform(),
	})
	if err != nil {
		return nil, ConnectErrorForImageBackend("pull image", imageRef, err)
	}
	return connect.NewResponse(&agentcomposev2.PullImageResponse{
		Image:       result.Image,
		Status:      agentcomposev2.ImageOperationStatus_IMAGE_OPERATION_STATUS_SUCCEEDED,
		ResolvedRef: result.ResolvedRef,
		Progress:    result.Progress,
		Warnings:    result.Warnings,
	}), nil
}

func (h *ImageHandler) InspectImage(ctx context.Context, req *connect.Request[agentcomposev2.InspectImageRequest]) (*connect.Response[agentcomposev2.InspectImageResponse], error) {
	imageRef := strings.TrimSpace(req.Msg.GetImageRef())
	if imageRef == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("image_ref is required"))
	}
	backend, err := h.backends.ImageBackendForStore(req.Msg.GetStore())
	if err != nil {
		return nil, err
	}
	result, err := backend.InspectImage(ctx, images.InspectRequest{ImageRef: imageRef})
	if err != nil {
		return nil, ConnectErrorForImageBackend("inspect image", imageRef, err)
	}
	return connect.NewResponse(&agentcomposev2.InspectImageResponse{
		Image:       result.Image,
		StoreStatus: result.StoreStatus,
	}), nil
}

func (h *ImageHandler) RemoveImage(ctx context.Context, req *connect.Request[agentcomposev2.RemoveImageRequest]) (*connect.Response[agentcomposev2.RemoveImageResponse], error) {
	imageRef := strings.TrimSpace(req.Msg.GetImageRef())
	if imageRef == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("image_ref is required"))
	}
	backend, err := h.backends.ImageBackendForStore(req.Msg.GetStore())
	if err != nil {
		return nil, err
	}
	result, err := backend.RemoveImage(ctx, images.RemoveRequest{
		ImageRef:      imageRef,
		Force:         req.Msg.GetForce(),
		PruneChildren: req.Msg.GetPruneChildren(),
	})
	if err != nil {
		return nil, ConnectErrorForImageBackend("remove image", imageRef, err)
	}
	return connect.NewResponse(&agentcomposev2.RemoveImageResponse{
		ImageRef:     result.ImageRef,
		UntaggedRefs: result.UntaggedRefs,
		DeletedIds:   result.DeletedIDs,
		Warnings:     result.Warnings,
	}), nil
}

func ConnectErrorForImageBackend(op, imageRef string, err error) error {
	if err == nil {
		return nil
	}
	if backendErr, kind, ok := images.ClassifyBackendError(err); ok {
		if imageRef != "" && backendErr.ImageRef == "" {
			backendErr.ImageRef = imageRef
		}
		if op != "" && backendErr.Op == "" {
			backendErr.Op = op
		}
		code := connect.CodeUnavailable
		switch kind {
		case images.ErrorKindNotFound:
			code = connect.CodeNotFound
		case images.ErrorKindInvalidReference:
			code = connect.CodeInvalidArgument
		case images.ErrorKindConflict:
			code = connect.CodeFailedPrecondition
		case images.ErrorKindInternal:
			code = connect.CodeInternal
		case images.ErrorKindUnavailable:
			code = connect.CodeUnavailable
		}
		return connect.NewError(code, backendErr)
	}
	return connect.NewError(connect.CodeUnknown, err)
}
