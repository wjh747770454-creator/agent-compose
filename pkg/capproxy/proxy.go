package capproxy

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/encoding"
	"google.golang.org/grpc/metadata"
	reflectionpb "google.golang.org/grpc/reflection/grpc_reflection_v1"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/dynamicpb"
)

const (
	SandboxTokenMetadata           = "x-capability-sandbox-token"
	deprecatedSessionTokenMetadata = "x-capability-session-token"
	maxJournalFrameBytes           = 1 << 20
	maxJournalStringBytes          = 16 << 10
	journalRedactedValue           = "[redacted]"
	journalRedactedContent         = "[redacted content]"
)

type SandboxBinding struct {
	SandboxID string
	// CapsetIDs is the set of capsets the sandbox is allowed to use. The guest
	// picks one per call (x-octobus-capset); capproxy validates membership.
	CapsetIDs []string
	Lineage   SandboxLineage
}

type SandboxLineage struct {
	ComposeProjectID string
	AgentName        string
	RunID            string
	RootRunID        string
	ParentRunID      string
}

type SandboxResolver interface {
	ResolveCapabilitySandbox(ctx context.Context, token string) (SandboxBinding, error)
}

// OctoBusResolver returns the current OctoBus dial target and token. ok is
// false when the gateway is not configured, so the data plane stays in sync
// with page edits without a restart.
type OctoBusResolver func(ctx context.Context) (addr string, token string, ok bool)

type Server struct {
	listen        string
	octobus       OctoBusResolver
	sandboxes     SandboxResolver
	journalPath   string
	journalLogger *slog.Logger
	journalMu     sync.Mutex
	descriptorMu  sync.RWMutex
	descriptors   map[string]methodPayloadDescriptors
	grpcServer    *grpc.Server
}

type Config struct {
	Listen                string
	OctoBus               OctoBusResolver
	InvocationJournalPath string
}

func NewServer(config Config, sandboxes SandboxResolver) *Server {
	return &Server{
		listen:      strings.TrimSpace(config.Listen),
		octobus:     config.OctoBus,
		sandboxes:   sandboxes,
		journalPath: strings.TrimSpace(config.InvocationJournalPath),
		descriptors: map[string]methodPayloadDescriptors{},
	}
}

func (s *Server) Configured() bool {
	return s != nil && s.listen != "" && s.octobus != nil && s.sandboxes != nil
}

func (s *Server) Serve(ctx context.Context) error {
	if !s.Configured() {
		return nil
	}
	ln, err := net.Listen("tcp", s.listen)
	if err != nil {
		return err
	}
	return s.serve(ctx, ln)
}

func (s *Server) serve(ctx context.Context, ln net.Listener) error {
	s.grpcServer = grpc.NewServer(grpc.ForceServerCodec(rawCodec{}), grpc.UnknownServiceHandler(s.handleUnknown))
	errCh := make(chan error, 1)
	go func() { errCh <- s.grpcServer.Serve(ln) }()
	select {
	case <-ctx.Done():
		s.grpcServer.GracefulStop()
		return nil
	case err := <-errCh:
		if err == nil {
			return nil
		}
		return err
	}
}

func (s *Server) handleUnknown(_ any, stream grpc.ServerStream) error {
	method, ok := grpc.MethodFromServerStream(stream)
	if !ok {
		return status.Error(codes.Internal, "missing gRPC method")
	}
	binding, err := s.resolveSandbox(stream.Context())
	if err != nil {
		return err
	}
	// The guest picks which capset this call targets (x-octobus-capset); capproxy
	// validates it is one the sandbox is allowed to use. Both the reflection and
	// business paths require a resolved capset.
	capset, err := resolveCallCapset(stream.Context(), binding.CapsetIDs)
	if err != nil {
		return err
	}
	outgoing := buildOutgoingMetadata(stream.Context(), capset)
	if !isReflectionMethod(method) {
		// Business calls route by capset + instance + method. The instance comes
		// from the injected guide.
		if firstMetadata(outgoing, "x-octobus-instance") == "" {
			return status.Error(codes.FailedPrecondition, "x-octobus-instance is required")
		}
	}
	return s.proxyStream(stream, method, outgoing, binding, capset)
}

// resolveCallCapset picks the capset for this call: the guest-supplied
// x-octobus-capset if it is in the allowed set, or the sole allowed capset when
// the guest omits it. Otherwise it is an error (the guest must disambiguate).
func resolveCallCapset(ctx context.Context, allowed []string) (string, error) {
	md, _ := metadata.FromIncomingContext(ctx)
	requested := firstMetadata(md, "x-octobus-capset")
	if requested != "" {
		if containsString(allowed, requested) {
			return requested, nil
		}
		return "", status.Errorf(codes.PermissionDenied, "capset %q is not allowed for this sandbox", requested)
	}
	if len(allowed) == 1 {
		return allowed[0], nil
	}
	return "", status.Error(codes.FailedPrecondition, "x-octobus-capset is required: sandbox allows multiple capsets")
}

func containsString(values []string, target string) bool {
	for _, v := range values {
		if v == target {
			return true
		}
	}
	return false
}

// buildOutgoingMetadata forwards the guest's incoming metadata to OctoBus,
// except agent-compose's own sandbox credential and any authorization (OctoBus
// auth is injected in proxyStream).
// x-octobus-capset is forced to the resolved, sandbox-allowed value so the guest
// cannot reach a capset outside its set.
func buildOutgoingMetadata(ctx context.Context, capset string) metadata.MD {
	incoming, _ := metadata.FromIncomingContext(ctx)
	outgoing := incoming.Copy()
	outgoing.Delete(SandboxTokenMetadata)
	outgoing.Delete(deprecatedSessionTokenMetadata)
	outgoing.Delete("authorization")
	outgoing.Set("x-octobus-capset", capset)
	return outgoing
}

func (s *Server) resolveSandbox(ctx context.Context) (SandboxBinding, error) {
	md, _ := metadata.FromIncomingContext(ctx)
	token := firstMetadata(md, SandboxTokenMetadata)
	if token == "" {
		token = firstMetadata(md, deprecatedSessionTokenMetadata)
	}
	if token == "" {
		token = bearerToken(firstMetadata(md, "authorization"))
	}
	if token == "" {
		return SandboxBinding{}, status.Error(codes.Unauthenticated, "missing capability sandbox token")
	}
	binding, err := s.sandboxes.ResolveCapabilitySandbox(ctx, token)
	if err != nil {
		return SandboxBinding{}, status.Error(codes.Unauthenticated, err.Error())
	}
	if len(binding.CapsetIDs) == 0 {
		return SandboxBinding{}, status.Error(codes.FailedPrecondition, "sandbox has no capability capset")
	}
	return binding, nil
}

func (s *Server) proxyStream(client grpc.ServerStream, method string, outgoing metadata.MD, binding SandboxBinding, capset string) error {
	startedAt := time.Now().UTC()
	addr, token, ok := s.octobus(client.Context())
	if !ok {
		return status.Error(codes.Unavailable, "capability gateway is not configured")
	}
	if token != "" {
		outgoing.Set("authorization", "Bearer "+token)
	}
	conn, err := grpc.NewClient(normalizeGRPCTarget(addr), grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithDefaultCallOptions(grpc.ForceCodec(rawCodec{})))
	if err != nil {
		return status.Error(codes.Unavailable, err.Error())
	}
	defer func() { _ = conn.Close() }()
	ctx := metadata.NewOutgoingContext(client.Context(), outgoing)
	desc := &grpc.StreamDesc{StreamName: strings.TrimPrefix(method, "/"), ServerStreams: true, ClientStreams: true}
	backend, err := conn.NewStream(ctx, desc, method)
	if err != nil {
		return err
	}
	errCh := make(chan error, 2)
	var requestFrame []byte
	var responseFrame []byte
	go func() {
		for {
			frame := rawFrame(nil)
			err := client.RecvMsg(&frame)
			if err == io.EOF {
				errCh <- backend.CloseSend()
				return
			}
			if err != nil {
				errCh <- err
				return
			}
			if requestFrame == nil && len(frame) <= maxJournalFrameBytes {
				requestFrame = append([]byte(nil), frame...)
			}
			if err := backend.SendMsg(&frame); err != nil {
				errCh <- err
				return
			}
		}
	}()
	go func() {
		for {
			frame := rawFrame(nil)
			err := backend.RecvMsg(&frame)
			if err == io.EOF {
				errCh <- nil
				return
			}
			if err != nil {
				errCh <- err
				return
			}
			if responseFrame == nil && len(frame) <= maxJournalFrameBytes {
				responseFrame = append([]byte(nil), frame...)
			}
			if err := client.SendMsg(&frame); err != nil {
				errCh <- err
				return
			}
		}
	}()
	var first error
	for i := 0; i < 2; i++ {
		if err := <-errCh; err != nil && first == nil {
			first = err
		}
	}
	s.recordInvocation(client.Context(), addr, method, outgoing, binding, capset, startedAt, first, requestFrame, responseFrame)
	return first
}

type methodPayloadDescriptors struct {
	Input  protoreflect.MessageDescriptor
	Output protoreflect.MessageDescriptor
}

type invocationJournalRecord struct {
	SchemaVersion     string                   `json:"schemaVersion"`
	RecordType        string                   `json:"recordType"`
	InvocationID      string                   `json:"invocationId"`
	Transport         string                   `json:"transport"`
	CapsetID          string                   `json:"capsetId"`
	ServiceID         string                   `json:"serviceId"`
	InstanceID        string                   `json:"instanceId"`
	Method            string                   `json:"method"`
	Lineage           invocationJournalLineage `json:"lineage"`
	Request           map[string]any           `json:"request"`
	Response          map[string]any           `json:"response"`
	GRPCCode          string                   `json:"grpcCode"`
	Success           bool                     `json:"success"`
	Error             *invocationJournalError  `json:"error"`
	StartedAt         string                   `json:"startedAt"`
	CompletedAt       string                   `json:"completedAt"`
	DurationMs        int64                    `json:"durationMs"`
	BusinessRequestID *string                  `json:"businessRequestId"`
}

type invocationJournalLineage struct {
	SandboxID        *string `json:"sandboxId"`
	ComposeProjectID *string `json:"composeProjectId"`
	AgentName        *string `json:"agentName"`
	RunID            *string `json:"runId"`
	RootRunID        *string `json:"rootRunId"`
	ParentRunID      *string `json:"parentRunId"`
}

type invocationJournalError struct {
	Type      string `json:"type"`
	Message   string `json:"message"`
	Retryable bool   `json:"retryable"`
}

func (s *Server) recordInvocation(ctx context.Context, addr string, method string, outgoing metadata.MD, binding SandboxBinding, capset string, startedAt time.Time, callErr error, requestFrame, responseFrame []byte) {
	if s == nil || s.journalPath == "" || isReflectionMethod(method) {
		return
	}
	completedAt := time.Now().UTC()
	serviceID, normalizedMethod := splitFullMethod(method)
	code := status.Code(callErr)
	success := callErr == nil
	request, response := s.decodeInvocationPayloads(ctx, addr, method, outgoing, requestFrame, responseFrame)
	record := invocationJournalRecord{
		SchemaVersion: "1.0",
		RecordType:    "capability_invocation",
		InvocationID:  uuid.NewString(),
		Transport:     "grpc",
		CapsetID:      strings.TrimSpace(capset),
		ServiceID:     serviceID,
		InstanceID:    firstMetadata(outgoing, "x-octobus-instance"),
		Method:        normalizedMethod,
		Lineage: invocationJournalLineage{
			SandboxID:        stringPtrOrNil(binding.SandboxID),
			ComposeProjectID: stringPtrOrNil(binding.Lineage.ComposeProjectID),
			AgentName:        stringPtrOrNil(binding.Lineage.AgentName),
			RunID:            stringPtrOrNil(binding.Lineage.RunID),
			RootRunID:        stringPtrOrNil(firstNonEmpty(binding.Lineage.RootRunID, binding.Lineage.RunID)),
			ParentRunID:      stringPtrOrNil(binding.Lineage.ParentRunID),
		},
		Request:           request,
		Response:          response,
		GRPCCode:          code.String(),
		Success:           success,
		Error:             invocationError(callErr, code),
		StartedAt:         startedAt.Format(time.RFC3339Nano),
		CompletedAt:       completedAt.Format(time.RFC3339Nano),
		DurationMs:        maxInt64(0, completedAt.Sub(startedAt).Milliseconds()),
		BusinessRequestID: stringPtrOrNil(firstMetadata(outgoing, "x-business-request-id")),
	}
	if record.ServiceID == "" {
		record.ServiceID = "unknown"
	}
	if record.Method == "" {
		record.Method = strings.TrimPrefix(method, "/")
	}
	if record.InstanceID == "" {
		record.InstanceID = "unknown"
	}
	s.journalMu.Lock()
	err := appendInvocationJournal(context.WithoutCancel(ctx), s.journalPath, record)
	s.journalMu.Unlock()
	if err != nil {
		s.logJournalError("capability invocation journal write failed", err)
	}
}

func (s *Server) decodeInvocationPayloads(ctx context.Context, addr, method string, outgoing metadata.MD, requestFrame, responseFrame []byte) (map[string]any, map[string]any) {
	request := map[string]any{}
	var response map[string]any
	if len(requestFrame) == 0 && len(responseFrame) == 0 {
		return request, response
	}
	descriptors, err := s.resolveMethodPayloadDescriptors(ctx, addr, method, outgoing)
	if err != nil {
		s.logJournalError("capability invocation payload descriptor resolution failed", err)
		return request, response
	}
	if decoded, ok := decodeProtoFrame(requestFrame, descriptors.Input); ok {
		request = sanitizeJournalPayload(decoded)
	}
	if decoded, ok := decodeProtoFrame(responseFrame, descriptors.Output); ok {
		response = sanitizeJournalPayload(decoded)
	}
	return request, response
}

func (s *Server) resolveMethodPayloadDescriptors(ctx context.Context, addr, method string, outgoing metadata.MD) (methodPayloadDescriptors, error) {
	method = strings.TrimPrefix(strings.TrimSpace(method), "/")
	if method == "" {
		return methodPayloadDescriptors{}, fmt.Errorf("method is required")
	}
	s.descriptorMu.RLock()
	if descriptors, ok := s.descriptors[method]; ok {
		s.descriptorMu.RUnlock()
		return descriptors, nil
	}
	s.descriptorMu.RUnlock()

	descriptors, err := fetchMethodPayloadDescriptors(ctx, addr, method, outgoing)
	if err != nil {
		return methodPayloadDescriptors{}, err
	}
	s.descriptorMu.Lock()
	if s.descriptors == nil {
		s.descriptors = map[string]methodPayloadDescriptors{}
	}
	s.descriptors[method] = descriptors
	s.descriptorMu.Unlock()
	return descriptors, nil
}

func fetchMethodPayloadDescriptors(ctx context.Context, addr, method string, outgoing metadata.MD) (methodPayloadDescriptors, error) {
	serviceName, methodName, ok := strings.Cut(strings.TrimPrefix(method, "/"), "/")
	if !ok || serviceName == "" || methodName == "" {
		return methodPayloadDescriptors{}, fmt.Errorf("invalid gRPC method %q", method)
	}
	reflectionMD := outgoing.Copy()
	reflectionCtx, cancel := context.WithTimeout(metadata.NewOutgoingContext(context.WithoutCancel(ctx), reflectionMD), 10*time.Second)
	defer cancel()
	conn, err := grpc.NewClient(normalizeGRPCTarget(addr), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return methodPayloadDescriptors{}, err
	}
	defer func() { _ = conn.Close() }()
	stream, err := reflectionpb.NewServerReflectionClient(conn).ServerReflectionInfo(reflectionCtx)
	if err != nil {
		return methodPayloadDescriptors{}, err
	}
	reflectionSymbol := serviceName + "." + methodName
	if err := stream.Send(&reflectionpb.ServerReflectionRequest{
		MessageRequest: &reflectionpb.ServerReflectionRequest_FileContainingSymbol{
			FileContainingSymbol: reflectionSymbol,
		},
	}); err != nil {
		return methodPayloadDescriptors{}, err
	}
	resp, err := stream.Recv()
	if err != nil {
		return methodPayloadDescriptors{}, err
	}
	fileResp := resp.GetFileDescriptorResponse()
	if fileResp == nil {
		return methodPayloadDescriptors{}, fmt.Errorf("reflection did not return descriptors for %s", serviceName)
	}
	files := make([]*descriptorpb.FileDescriptorProto, 0, len(fileResp.FileDescriptorProto))
	for _, raw := range fileResp.FileDescriptorProto {
		file := &descriptorpb.FileDescriptorProto{}
		if err := proto.Unmarshal(raw, file); err != nil {
			return methodPayloadDescriptors{}, fmt.Errorf("decode reflected descriptor: %w", err)
		}
		files = append(files, file)
	}
	registry, err := protodesc.NewFiles(&descriptorpb.FileDescriptorSet{File: files})
	if err != nil {
		return methodPayloadDescriptors{}, fmt.Errorf("build reflected descriptors: %w", err)
	}
	desc, err := registry.FindDescriptorByName(protoreflect.FullName(serviceName))
	if err != nil {
		return methodPayloadDescriptors{}, fmt.Errorf("find reflected service %s: %w", serviceName, err)
	}
	service, ok := desc.(protoreflect.ServiceDescriptor)
	if !ok {
		return methodPayloadDescriptors{}, fmt.Errorf("%s is not a service descriptor", serviceName)
	}
	methodDesc := service.Methods().ByName(protoreflect.Name(methodName))
	if methodDesc == nil {
		return methodPayloadDescriptors{}, fmt.Errorf("find reflected method %s: %w", method, protoregistry.NotFound)
	}
	return methodPayloadDescriptors{Input: methodDesc.Input(), Output: methodDesc.Output()}, nil
}

func decodeProtoFrame(frame []byte, descriptor protoreflect.MessageDescriptor) (map[string]any, bool) {
	if len(frame) == 0 || descriptor == nil {
		return nil, false
	}
	msg := dynamicpb.NewMessage(descriptor)
	if err := proto.Unmarshal(frame, msg); err != nil {
		return nil, false
	}
	data, err := protojson.MarshalOptions{UseProtoNames: false}.Marshal(msg)
	if err != nil {
		return nil, false
	}
	var decoded map[string]any
	if err := json.Unmarshal(data, &decoded); err != nil {
		return nil, false
	}
	if decoded == nil {
		decoded = map[string]any{}
	}
	return decoded, true
}

func sanitizeJournalPayload(payload map[string]any) map[string]any {
	if payload == nil {
		return nil
	}
	return sanitizeJournalValue(payload, "").(map[string]any)
}

func sanitizeJournalValue(value any, fieldName string) any {
	if isSensitiveJournalField(fieldName) {
		return journalRedactedValue
	}
	if isJournalContentField(fieldName) {
		return journalRedactedContent
	}
	switch typed := value.(type) {
	case map[string]any:
		clean := make(map[string]any, len(typed))
		for key, item := range typed {
			clean[key] = sanitizeJournalValue(item, key)
		}
		return clean
	case []any:
		clean := make([]any, len(typed))
		for index, item := range typed {
			clean[index] = sanitizeJournalValue(item, fieldName)
		}
		return clean
	case string:
		return truncateJournalString(typed)
	default:
		return value
	}
}

func normalizedJournalField(fieldName string) string {
	fieldName = strings.ToLower(strings.TrimSpace(fieldName))
	replacer := strings.NewReplacer("_", "", "-", "", ".", "", " ", "")
	return replacer.Replace(fieldName)
}

func isSensitiveJournalField(fieldName string) bool {
	fieldName = normalizedJournalField(fieldName)
	if fieldName == "" {
		return false
	}
	for _, marker := range []string{
		"authorization", "password", "passwd", "secret", "token", "cookie",
		"apikey", "accesskey", "privatekey", "credential", "encryptionkey",
	} {
		if strings.Contains(fieldName, marker) {
			return true
		}
	}
	return false
}

func isJournalContentField(fieldName string) bool {
	switch normalizedJournalField(fieldName) {
	case "content", "body", "raw", "html", "markdown", "transcript", "filecontent", "messagecontent", "attachment", "binary", "bytes":
		return true
	default:
		return false
	}
}

func truncateJournalString(value string) string {
	value = strings.ToValidUTF8(value, "\uFFFD")
	if len(value) <= maxJournalStringBytes {
		return value
	}
	var builder strings.Builder
	for _, char := range value {
		if builder.Len()+len(string(char)) > maxJournalStringBytes {
			break
		}
		builder.WriteRune(char)
	}
	return builder.String() + "[truncated]"
}

func invocationError(err error, code codes.Code) *invocationJournalError {
	if err == nil {
		return nil
	}
	return &invocationJournalError{
		Type:      code.String(),
		Message:   "capability returned " + code.String(),
		Retryable: code == codes.Unavailable || code == codes.DeadlineExceeded || code == codes.ResourceExhausted,
	}
}

func appendInvocationJournal(ctx context.Context, path string, record invocationJournalRecord) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create invocation journal directory: %w", err)
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("open invocation journal: %w", err)
	}
	defer func() { _ = file.Close() }()
	encoder := json.NewEncoder(file)
	if err := encoder.Encode(record); err != nil {
		return fmt.Errorf("append invocation journal: %w", err)
	}
	return nil
}

func (s *Server) logJournalError(message string, err error) {
	logger := s.journalLogger
	if logger == nil {
		logger = slog.Default()
	}
	logger.Warn(message, "error", err)
}

func splitFullMethod(method string) (string, string) {
	method = strings.TrimPrefix(strings.TrimSpace(method), "/")
	service, name, ok := strings.Cut(method, "/")
	if !ok {
		return "", method
	}
	return strings.TrimSpace(service), strings.TrimSpace(service + "/" + name)
}

func stringPtrOrNil(value string) *string {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	return &value
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func maxInt64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

func isReflectionMethod(method string) bool {
	return strings.HasPrefix(method, "/grpc.reflection.v1.") || strings.HasPrefix(method, "/grpc.reflection.v1alpha.")
}

func firstMetadata(md metadata.MD, key string) string {
	values := md.Get(key)
	if len(values) == 0 {
		return ""
	}
	return strings.TrimSpace(values[0])
}

func bearerToken(value string) string {
	if !strings.HasPrefix(strings.ToLower(value), "bearer ") {
		return ""
	}
	return strings.TrimSpace(value[len("bearer "):])
}

func normalizeGRPCTarget(raw string) string {
	raw = strings.TrimRight(strings.TrimSpace(raw), "/")
	if raw == "" {
		return ""
	}
	parsed, err := url.Parse(raw)
	if err == nil && parsed.Host != "" {
		return parsed.Host
	}
	return raw
}

var _ encoding.Codec = rawCodec{}

type rawFrame []byte

type rawCodec struct{}

func (rawCodec) Name() string { return "proto" }

func (rawCodec) Marshal(v any) ([]byte, error) {
	switch x := v.(type) {
	case *rawFrame:
		return []byte(*x), nil
	case rawFrame:
		return []byte(x), nil
	case proto.Message:
		return proto.Marshal(x)
	default:
		return nil, fmt.Errorf("unsupported raw marshal type %T", v)
	}
}

func (rawCodec) Unmarshal(data []byte, v any) error {
	switch x := v.(type) {
	case *rawFrame:
		*x = append((*x)[:0], data...)
		return nil
	case proto.Message:
		return proto.Unmarshal(data, x)
	default:
		return fmt.Errorf("unsupported raw unmarshal type %T", v)
	}
}
