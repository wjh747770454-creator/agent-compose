package capproxy

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/dynamicpb"
	"google.golang.org/protobuf/types/known/emptypb"
)

type testResolver struct {
	binding SandboxBinding
}

func (r testResolver) ResolveCapabilitySandbox(_ context.Context, token string) (SandboxBinding, error) {
	if token != "sandbox-token" {
		return SandboxBinding{}, status.Error(codes.Unauthenticated, "bad token")
	}
	return r.binding, nil
}

func staticOctoBus(addr, token string) OctoBusResolver {
	return func(context.Context) (string, string, bool) {
		return addr, token, true
	}
}

func TestProxyInjectsOctoBusMetadata(t *testing.T) {
	var received metadata.MD
	octoAddr, stopOcto := startTestRawGRPC(t, func(_ any, stream grpc.ServerStream) error {
		received, _ = metadata.FromIncomingContext(stream.Context())
		req := rawFrame(nil)
		if err := stream.RecvMsg(&req); err != nil {
			return err
		}
		return stream.SendMsg(rawFrame("ok:" + string(req)))
	})
	defer stopOcto()
	proxyAddr, stopProxy := startTestProxy(t, Config{Listen: "127.0.0.1:0", OctoBus: staticOctoBus(octoAddr, "octo-token")}, testResolver{binding: SandboxBinding{SandboxID: "s1", CapsetIDs: []string{"dev"}}})
	defer stopProxy()

	conn, err := grpc.NewClient(proxyAddr, grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithDefaultCallOptions(grpc.ForceCodec(rawCodec{})))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	ctx := metadata.NewOutgoingContext(context.Background(), metadata.Pairs(
		SandboxTokenMetadata, "sandbox-token",
		"x-octobus-instance", "inst",
	))
	out := rawFrame(nil)
	if err := conn.Invoke(ctx, "/pkg.Service/Call", rawFrame("ping"), &out); err != nil {
		t.Fatal(err)
	}
	if string(out) != "ok:ping" {
		t.Fatalf("unexpected response %q", string(out))
	}
	for key, want := range map[string]string{
		"x-octobus-capset":   "dev",
		"x-octobus-instance": "inst",
		"authorization":      "Bearer octo-token",
	} {
		if got := firstMetadata(received, key); got != want {
			t.Fatalf("metadata %s = %q, want %q", key, got, want)
		}
	}
}

func TestProxyJournalsSuccessfulInvocation(t *testing.T) {
	journalPath := filepath.Join(t.TempDir(), "invocations.jsonl")
	octoAddr, stopOcto := startTestRawGRPC(t, func(_ any, stream grpc.ServerStream) error {
		req := rawFrame(nil)
		if err := stream.RecvMsg(&req); err != nil {
			return err
		}
		return stream.SendMsg(rawFrame("ok:" + string(req)))
	})
	defer stopOcto()
	proxyAddr, stopProxy := startTestProxy(t, Config{
		Listen:                "127.0.0.1:0",
		OctoBus:               staticOctoBus(octoAddr, ""),
		InvocationJournalPath: journalPath,
	}, testResolver{binding: SandboxBinding{
		SandboxID: "sandbox-1",
		CapsetIDs: []string{"dev"},
		Lineage: SandboxLineage{
			ComposeProjectID: "office",
			AgentName:        "entry",
			RunID:            "run-1",
		},
	}})
	defer stopProxy()

	conn, err := grpc.NewClient(proxyAddr, grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithDefaultCallOptions(grpc.ForceCodec(rawCodec{})))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	ctx := metadata.NewOutgoingContext(context.Background(), metadata.Pairs(
		SandboxTokenMetadata, "sandbox-token",
		"x-octobus-instance", "crm-prod",
		"x-business-request-id", "biz-1",
	))
	out := rawFrame(nil)
	if err := conn.Invoke(ctx, "/chaitin.crm.v1.Chaitin_CRM/ListProjects", rawFrame("ping"), &out); err != nil {
		t.Fatal(err)
	}

	record := readOneJournalRecord(t, journalPath)
	if record.SchemaVersion != "1.0" || record.RecordType != "capability_invocation" {
		t.Fatalf("unexpected journal envelope: %#v", record)
	}
	if record.CapsetID != "dev" || record.ServiceID != "chaitin.crm.v1.Chaitin_CRM" || record.InstanceID != "crm-prod" {
		t.Fatalf("unexpected routing fields: %#v", record)
	}
	if record.Method != "chaitin.crm.v1.Chaitin_CRM/ListProjects" || record.GRPCCode != codes.OK.String() || !record.Success {
		t.Fatalf("unexpected result fields: %#v", record)
	}
	if record.Lineage.SandboxID == nil || *record.Lineage.SandboxID != "sandbox-1" {
		t.Fatalf("sandbox lineage = %#v, want sandbox-1", record.Lineage.SandboxID)
	}
	if record.Lineage.RunID == nil || *record.Lineage.RunID != "run-1" || record.Lineage.RootRunID == nil || *record.Lineage.RootRunID != "run-1" {
		t.Fatalf("run lineage = %#v", record.Lineage)
	}
	if record.BusinessRequestID == nil || *record.BusinessRequestID != "biz-1" {
		t.Fatalf("business request id = %#v, want biz-1", record.BusinessRequestID)
	}
}

func TestProxyJournalsBackendErrorInvocation(t *testing.T) {
	journalPath := filepath.Join(t.TempDir(), "invocations.jsonl")
	octoAddr, stopOcto := startTestRawGRPC(t, func(_ any, stream grpc.ServerStream) error {
		req := rawFrame(nil)
		if err := stream.RecvMsg(&req); err != nil {
			return err
		}
		return status.Error(codes.InvalidArgument, "bad request")
	})
	defer stopOcto()
	proxyAddr, stopProxy := startTestProxy(t, Config{
		Listen:                "127.0.0.1:0",
		OctoBus:               staticOctoBus(octoAddr, ""),
		InvocationJournalPath: journalPath,
	}, testResolver{binding: SandboxBinding{SandboxID: "sandbox-1", CapsetIDs: []string{"dev"}}})
	defer stopProxy()

	conn, err := grpc.NewClient(proxyAddr, grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithDefaultCallOptions(grpc.ForceCodec(rawCodec{})))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	ctx := metadata.NewOutgoingContext(context.Background(), metadata.Pairs(
		SandboxTokenMetadata, "sandbox-token",
		"x-octobus-instance", "todo-prod",
	))
	out := rawFrame(nil)
	err = conn.Invoke(ctx, "/dingtalk.todo.v1.TodoService/CreateTodo", rawFrame("ping"), &out)
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("code = %s, want %s; err=%v", status.Code(err), codes.InvalidArgument, err)
	}

	record := readOneJournalRecord(t, journalPath)
	if record.Success || record.GRPCCode != codes.InvalidArgument.String() {
		t.Fatalf("unexpected error result: %#v", record)
	}
	if record.Error == nil || record.Error.Type != codes.InvalidArgument.String() || record.Error.Message != "capability returned InvalidArgument" {
		t.Fatalf("unexpected journal error: %#v", record.Error)
	}
	if strings.Contains(record.Error.Message, "bad request") {
		t.Fatalf("journal error leaked backend detail: %#v", record.Error)
	}
}

func TestSanitizeJournalPayloadRedactsSecretsAndContent(t *testing.T) {
	longTitle := strings.Repeat("项", maxJournalStringBytes)
	payload := map[string]any{
		"title":       longTitle,
		"accessToken": "token-value",
		"content":     "customer message body",
		"nested": map[string]any{
			"password":    "password-value",
			"projectId":   "project-1",
			"description": "short business summary",
		},
	}

	clean := sanitizeJournalPayload(payload)
	if clean["accessToken"] != journalRedactedValue || clean["content"] != journalRedactedContent {
		t.Fatalf("top-level journal redaction failed: %#v", clean)
	}
	nested := clean["nested"].(map[string]any)
	if nested["password"] != journalRedactedValue {
		t.Fatalf("nested journal redaction failed: %#v", nested)
	}
	if nested["projectId"] != "project-1" || nested["description"] != "short business summary" {
		t.Fatalf("journal redaction removed projection fields: %#v", nested)
	}
	if title := clean["title"].(string); len(title) <= maxJournalStringBytes || !strings.HasSuffix(title, "[truncated]") {
		t.Fatalf("journal string was not bounded: bytes=%d suffix=%q", len(title), title[len(title)-16:])
	}
}

func TestDecodeProtoFrameUsesJSONFieldNames(t *testing.T) {
	name := "payload.proto"
	pkg := "journal.test"
	syntax := "proto3"
	fieldName := "project_id"
	jsonName := "projectId"
	messageName := "Payload"
	fieldNumber := int32(1)
	fieldLabel := descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL
	fieldType := descriptorpb.FieldDescriptorProto_TYPE_STRING
	file, err := protodesc.NewFile(&descriptorpb.FileDescriptorProto{
		Name:    &name,
		Package: &pkg,
		Syntax:  &syntax,
		MessageType: []*descriptorpb.DescriptorProto{{
			Name: &messageName,
			Field: []*descriptorpb.FieldDescriptorProto{{
				Name:     &fieldName,
				JsonName: &jsonName,
				Number:   &fieldNumber,
				Label:    &fieldLabel,
				Type:     &fieldType,
			}},
		}},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	message := file.Messages().ByName(protoreflect.Name(messageName))
	payload := dynamicpb.NewMessage(message)
	payload.Set(message.Fields().ByName(protoreflect.Name(fieldName)), protoreflect.ValueOfString("crm-project-1"))
	frame, err := proto.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}

	decoded, ok := decodeProtoFrame(frame, message)
	if !ok {
		t.Fatal("decodeProtoFrame returned false")
	}
	if got := decoded[jsonName]; got != "crm-project-1" {
		t.Fatalf("decoded[%s] = %#v, want crm-project-1; decoded=%#v", jsonName, got, decoded)
	}
}

func TestProxyForwardsGuestInstance(t *testing.T) {
	var received metadata.MD
	octoAddr, stopOcto := startTestRawGRPC(t, func(_ any, stream grpc.ServerStream) error {
		received, _ = metadata.FromIncomingContext(stream.Context())
		req := rawFrame(nil)
		if err := stream.RecvMsg(&req); err != nil {
			return err
		}
		return stream.SendMsg(rawFrame("ok:" + string(req)))
	})
	defer stopOcto()
	proxyAddr, stopProxy := startTestProxy(t, Config{Listen: "127.0.0.1:0", OctoBus: staticOctoBus(octoAddr, "")}, testResolver{binding: SandboxBinding{SandboxID: "s1", CapsetIDs: []string{"dev"}}})
	defer stopProxy()

	conn, err := grpc.NewClient(proxyAddr, grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithDefaultCallOptions(grpc.ForceCodec(rawCodec{})))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	ctx := metadata.NewOutgoingContext(context.Background(), metadata.MD{
		SandboxTokenMetadata: []string{"sandbox-token"},
		"x-octobus-instance": []string{"guest-inst"},
		"x-octobus-capset":   []string{"dev"},
	})
	out := rawFrame(nil)
	if err := conn.Invoke(ctx, "/pkg.Service/Call", rawFrame("ping"), &out); err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]string{
		"x-octobus-capset":   "dev",
		"x-octobus-instance": "guest-inst",
	} {
		if got := firstMetadata(received, key); got != want {
			t.Fatalf("metadata %s = %q, want %q", key, got, want)
		}
	}
}

func TestProxyRejectsMissingInstanceForBusinessCall(t *testing.T) {
	octoAddr, stopOcto := startTestRawGRPC(t, func(_ any, stream grpc.ServerStream) error { return nil })
	defer stopOcto()
	proxyAddr, stopProxy := startTestProxy(t, Config{Listen: "127.0.0.1:0", OctoBus: staticOctoBus(octoAddr, "")}, testResolver{binding: SandboxBinding{SandboxID: "s1", CapsetIDs: []string{"dev"}}})
	defer stopProxy()

	conn, err := grpc.NewClient(proxyAddr, grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithDefaultCallOptions(grpc.ForceCodec(rawCodec{})))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	ctx := metadata.NewOutgoingContext(context.Background(), metadata.Pairs(SandboxTokenMetadata, "sandbox-token"))
	out := rawFrame(nil)
	err = conn.Invoke(ctx, "/pkg.Service/Call", rawFrame("ping"), &out)
	if status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("expected FailedPrecondition for missing instance, got %v", err)
	}
}

func TestProxyRejectsCapsetOutsideAllowedSet(t *testing.T) {
	octoAddr, stopOcto := startTestRawGRPC(t, func(_ any, stream grpc.ServerStream) error { return nil })
	defer stopOcto()
	proxyAddr, stopProxy := startTestProxy(t, Config{Listen: "127.0.0.1:0", OctoBus: staticOctoBus(octoAddr, "")}, testResolver{binding: SandboxBinding{SandboxID: "s1", CapsetIDs: []string{"dev"}}})
	defer stopProxy()

	conn, err := grpc.NewClient(proxyAddr, grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithDefaultCallOptions(grpc.ForceCodec(rawCodec{})))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	// Guest requests a capset the sandbox is not allowed to use.
	ctx := metadata.NewOutgoingContext(context.Background(), metadata.MD{
		SandboxTokenMetadata: []string{"sandbox-token"},
		"x-octobus-capset":   []string{"other"},
	})
	out := rawFrame(nil)
	err = conn.Invoke(ctx, "/pkg.Service/Call", rawFrame("ping"), &out)
	if status.Code(err) != codes.PermissionDenied {
		t.Fatalf("expected PermissionDenied for disallowed capset, got %v", err)
	}
}

func TestProxyRejectsMissingSandboxToken(t *testing.T) {
	octoAddr, stopOcto := startTestRawGRPC(t, func(_ any, stream grpc.ServerStream) error { return nil })
	defer stopOcto()
	proxyAddr, stopProxy := startTestProxy(t, Config{Listen: "127.0.0.1:0", OctoBus: staticOctoBus(octoAddr, "")}, testResolver{binding: SandboxBinding{SandboxID: "s1", CapsetIDs: []string{"dev"}}})
	defer stopProxy()

	conn, err := grpc.NewClient(proxyAddr, grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithDefaultCallOptions(grpc.ForceCodec(rawCodec{})))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	out := rawFrame(nil)
	err = conn.Invoke(context.Background(), "/pkg.Service/Call", rawFrame("ping"), &out)
	if status.Code(err) != codes.Unauthenticated {
		t.Fatalf("code = %s, want %s; err=%v", status.Code(err), codes.Unauthenticated, err)
	}
}

func TestServerConfiguredAndServeBranches(t *testing.T) {
	var nilServer *Server
	if nilServer.Configured() {
		t.Fatal("nil server should not be configured")
	}
	if NewServer(Config{Listen: "  ", OctoBus: staticOctoBus("127.0.0.1:1", "")}, testResolver{}).Configured() {
		t.Fatal("blank listen address should not be configured")
	}
	if NewServer(Config{Listen: "127.0.0.1:0", OctoBus: nil}, testResolver{}).Configured() {
		t.Fatal("nil OctoBus resolver should not be configured")
	}
	if NewServer(Config{Listen: "127.0.0.1:0", OctoBus: staticOctoBus("127.0.0.1:1", "")}, nil).Configured() {
		t.Fatal("nil sandbox resolver should not be configured")
	}
	if err := NewServer(Config{}, nil).Serve(context.Background()); err != nil {
		t.Fatalf("unconfigured Serve returned error: %v", err)
	}

	server := NewServer(Config{Listen: "127.0.0.1:bad", OctoBus: staticOctoBus("127.0.0.1:1", "")}, testResolver{})
	if !server.Configured() {
		t.Fatal("expected complete server to be configured")
	}
	if err := server.Serve(context.Background()); err == nil {
		t.Fatal("expected invalid listen address error")
	}
}

func TestResolveSandboxBearerFallbackAndErrors(t *testing.T) {
	server := NewServer(Config{}, testResolver{binding: SandboxBinding{SandboxID: "s1", CapsetIDs: []string{"dev"}}})
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", "Bearer sandbox-token"))
	binding, err := server.resolveSandbox(ctx)
	if err != nil {
		t.Fatalf("resolveSandbox returned error: %v", err)
	}
	if binding.SandboxID != "s1" {
		t.Fatalf("binding SandboxID = %q, want s1", binding.SandboxID)
	}

	ctx = metadata.NewIncomingContext(context.Background(), metadata.Pairs(deprecatedSessionTokenMetadata, "sandbox-token"))
	if _, err := server.resolveSandbox(ctx); err != nil {
		t.Fatalf("deprecated token header fallback returned error: %v", err)
	}

	ctx = metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", "Bearer bad-token"))
	if _, err := server.resolveSandbox(ctx); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("bad token code = %s, want %s; err=%v", status.Code(err), codes.Unauthenticated, err)
	}

	server = NewServer(Config{}, testResolver{binding: SandboxBinding{SandboxID: "s1"}})
	ctx = metadata.NewIncomingContext(context.Background(), metadata.Pairs(SandboxTokenMetadata, "sandbox-token"))
	if _, err := server.resolveSandbox(ctx); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("empty capset code = %s, want %s; err=%v", status.Code(err), codes.FailedPrecondition, err)
	}
}

func TestCapsetResolutionAndOutgoingMetadataHelpers(t *testing.T) {
	ctx := metadata.NewIncomingContext(context.Background(), metadata.MD{
		SandboxTokenMetadata: []string{"sandbox-token"},
		"authorization":      []string{"Bearer guest-token"},
		"x-octobus-capset":   []string{"old"},
		"x-custom":           []string{"kept"},
	})
	capset, err := resolveCallCapset(ctx, []string{"old", "other"})
	if err != nil {
		t.Fatalf("resolveCallCapset returned error: %v", err)
	}
	if capset != "old" {
		t.Fatalf("capset = %q, want old", capset)
	}

	ctx = metadata.NewIncomingContext(context.Background(), metadata.MD{})
	if _, err := resolveCallCapset(ctx, []string{"one", "two"}); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("ambiguous capset code = %s, want %s; err=%v", status.Code(err), codes.FailedPrecondition, err)
	}

	outgoing := buildOutgoingMetadata(metadata.NewIncomingContext(context.Background(), metadata.MD{
		SandboxTokenMetadata:           []string{"sandbox-token"},
		deprecatedSessionTokenMetadata: []string{"sandbox-token"},
		"authorization":                []string{"Bearer guest-token"},
		"x-octobus-capset":             []string{"old"},
		"x-custom":                     []string{"kept"},
	}), "new")
	if got := firstMetadata(outgoing, SandboxTokenMetadata); got != "" {
		t.Fatalf("sandbox token metadata was forwarded: %q", got)
	}
	if got := firstMetadata(outgoing, deprecatedSessionTokenMetadata); got != "" {
		t.Fatalf("deprecated token metadata was forwarded: %q", got)
	}
	if got := firstMetadata(outgoing, "authorization"); got != "" {
		t.Fatalf("authorization metadata was forwarded: %q", got)
	}
	if got := firstMetadata(outgoing, "x-octobus-capset"); got != "new" {
		t.Fatalf("capset metadata = %q, want new", got)
	}
	if got := firstMetadata(outgoing, "x-custom"); got != "kept" {
		t.Fatalf("custom metadata = %q, want kept", got)
	}
}

func TestProxyHelpersAndRawCodecBranches(t *testing.T) {
	if got := bearerToken("bearer  token "); got != "token" {
		t.Fatalf("bearer token = %q, want token", got)
	}
	if got := bearerToken("Basic token"); got != "" {
		t.Fatalf("non-bearer token = %q, want empty", got)
	}
	if got := normalizeGRPCTarget(" http://127.0.0.1:1234/ "); got != "127.0.0.1:1234" {
		t.Fatalf("normalized URL target = %q", got)
	}
	if got := normalizeGRPCTarget(" octobus:9000/ "); got != "octobus:9000" {
		t.Fatalf("normalized bare target = %q", got)
	}
	if got := normalizeGRPCTarget(" / "); got != "" {
		t.Fatalf("normalized empty target = %q", got)
	}
	if !isReflectionMethod("/grpc.reflection.v1.ServerReflection/ServerReflectionInfo") {
		t.Fatal("v1 reflection method was not detected")
	}
	if !isReflectionMethod("/grpc.reflection.v1alpha.ServerReflection/ServerReflectionInfo") {
		t.Fatal("v1alpha reflection method was not detected")
	}
	if isReflectionMethod("/pkg.Service/Call") {
		t.Fatal("business method detected as reflection")
	}

	codec := rawCodec{}
	data, err := codec.Marshal(rawFrame("raw"))
	if err != nil {
		t.Fatalf("raw marshal failed: %v", err)
	}
	if string(data) != "raw" {
		t.Fatalf("raw marshal = %q, want raw", string(data))
	}
	frame := rawFrame("ptr")
	data, err = codec.Marshal(&frame)
	if err != nil {
		t.Fatalf("raw pointer marshal failed: %v", err)
	}
	if string(data) != "ptr" {
		t.Fatalf("raw pointer marshal = %q, want ptr", string(data))
	}
	if _, err := codec.Marshal(123); err == nil {
		t.Fatal("expected unsupported marshal type error")
	}

	var out rawFrame
	if err := codec.Unmarshal([]byte("decoded"), &out); err != nil {
		t.Fatalf("raw unmarshal failed: %v", err)
	}
	if string(out) != "decoded" {
		t.Fatalf("raw unmarshal = %q, want decoded", string(out))
	}
	if _, err := codec.Marshal(&emptypb.Empty{}); err != nil {
		t.Fatalf("proto marshal failed: %v", err)
	}
	if err := codec.Unmarshal(nil, &emptypb.Empty{}); err != nil {
		t.Fatalf("proto unmarshal failed: %v", err)
	}
	if err := codec.Unmarshal(nil, new(int)); err == nil {
		t.Fatal("expected unsupported unmarshal type error")
	}
}

func startTestProxy(t *testing.T, config Config, resolver SandboxResolver) (string, func()) {
	t.Helper()
	ln, err := net.Listen("tcp", config.Listen)
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	config.Listen = addr
	server := NewServer(config, resolver)
	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() { errCh <- server.serve(ctx, ln) }()
	return addr, func() {
		cancel()
		if err := <-errCh; err != nil && !strings.Contains(err.Error(), "use of closed network connection") {
			t.Fatalf("proxy returned error: %v", err)
		}
	}
}

func startTestRawGRPC(t *testing.T, handler grpc.StreamHandler) (string, func()) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := grpc.NewServer(grpc.ForceServerCodec(rawCodec{}), grpc.UnknownServiceHandler(func(srv any, stream grpc.ServerStream) error {
		return handler(srv, stream)
	}))
	errCh := make(chan error, 1)
	go func() { errCh <- server.Serve(ln) }()
	return ln.Addr().String(), func() {
		server.Stop()
		if err := <-errCh; err != nil && !errors.Is(err, grpc.ErrServerStopped) && !strings.Contains(err.Error(), "use of closed network connection") {
			t.Fatalf("raw grpc returned error: %v", err)
		}
	}
}

func readOneJournalRecord(t *testing.T, path string) invocationJournalRecord {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 1 {
		t.Fatalf("journal line count = %d, want 1; data=%q", len(lines), string(data))
	}
	var record invocationJournalRecord
	if err := json.Unmarshal([]byte(lines[0]), &record); err != nil {
		t.Fatalf("decode journal record: %v; data=%q", err, lines[0])
	}
	return record
}
