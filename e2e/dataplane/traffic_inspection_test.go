//go:build e2e

package dataplane

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/fengqi-dev/kube-loop/e2e/harness"
	"github.com/fengqi-dev/kube-loop/internal/client/credentials"
	clientdataplane "github.com/fengqi-dev/kube-loop/internal/client/dataplane"
	"github.com/fengqi-dev/kube-loop/internal/client/profile"
	"github.com/fengqi-dev/kube-loop/internal/client/remote"
	"github.com/fengqi-dev/kube-loop/internal/controlplane"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/authorization"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/controlplaneapi"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/sessionapi"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/storage"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/ticketapi"
	"github.com/fengqi-dev/kube-loop/internal/protocol/networkspec"
	"github.com/fengqi-dev/kube-loop/internal/protocol/websocket"
	"github.com/fengqi-dev/kube-loop/internal/trafficinspect"
	"github.com/google/uuid"
	"golang.org/x/net/http2"
	"google.golang.org/protobuf/encoding/protowire"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	"k8s.io/utils/ptr"
)

const trafficInspectionAccessToken = "e2e-traffic-inspection"

const (
	trafficInspectionHTTPPath       = "/get?mode=dataplane-e2e"
	trafficInspectionSSEPath        = "/sse?count=2&duration=100ms&delay=0s"
	trafficInspectionWebSocketPath  = "/websocket/echo"
	trafficInspectionGRPCUnaryPath  = "/grpcbin.GRPCBin/DummyUnary"
	trafficInspectionGRPCStreamPath = "/grpcbin.GRPCBin/DummyServerStream"
)

const trafficInspectionGRPCBinTypesProto = `syntax = "proto3";
package grpcbin;

import "google/protobuf/timestamp.proto";

message DummyMessage {
  message Sub { string f_string = 1; }
  enum Enum { ENUM_0 = 0; ENUM_1 = 1; ENUM_2 = 2; }

  string f_string = 1;
  repeated string f_strings = 2;
  int32 f_int32 = 3;
  repeated int32 f_int32s = 4;
  Enum f_enum = 5;
  repeated Enum f_enums = 6;
  Sub f_sub = 7;
  repeated Sub f_subs = 8;
  bool f_bool = 9;
  repeated bool f_bools = 10;
  int64 f_int64 = 11;
  repeated int64 f_int64s = 12;
  bytes f_bytes = 13;
  repeated bytes f_bytess = 14;
  float f_float = 15;
  repeated float f_floats = 16;
  map<string, int32> f_map = 17;
  oneof choice {
    string f_choice_text = 18;
    int64 f_choice_number = 20;
  }
  google.protobuf.Timestamp f_timestamp = 19;
}
`

const trafficInspectionGRPCBinServiceProto = `syntax = "proto3";
package grpcbin;

import "grpcbin/types.proto";

service GRPCBin {
  rpc DummyUnary(DummyMessage) returns (DummyMessage);
  rpc DummyServerStream(DummyMessage) returns (stream DummyMessage);
}
`

func TestRealTrafficInspectionThroughDataPlane(t *testing.T) {
	harness.RequireE2E(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	kubeClient := kubeClient(t)
	if err := harness.EnsureEchoWorkload(ctx, kubeClient); err != nil {
		t.Fatalf("ensure traffic inspection namespace: %v", err)
	}
	backends := installTrafficInspectionBackends(t, ctx, kubeClient)
	dialContext, output := startTrafficInspectionDataPlane(t, ctx, kubeClient, backends.nodeIP)

	transport := &http.Transport{DialContext: dialContext}
	t.Cleanup(transport.CloseIdleConnections)
	response, err := (&http.Client{Transport: transport}).Get("http://" + backends.httpAddress + trafficInspectionHTTPPath)
	if err != nil {
		t.Fatalf("call go-httpbin through Data Plane: %v", err)
	}
	var payload struct {
		Arguments map[string][]string `json:"args"`
		Method    string              `json:"method"`
	}
	decodeErr := json.NewDecoder(response.Body).Decode(&payload)
	closeErr := response.Body.Close()
	if decodeErr != nil || closeErr != nil {
		t.Fatal(decodeErr, closeErr)
	}
	if response.StatusCode != http.StatusOK || payload.Method != http.MethodGet ||
		len(payload.Arguments["mode"]) != 1 || payload.Arguments["mode"][0] != "dataplane-e2e" {
		t.Fatalf("unexpected go-httpbin response: status=%d payload=%#v", response.StatusCode, payload)
	}
	callSSEThroughDataPlane(t, transport, backends.httpAddress)
	callWebSocketThroughDataPlane(t, transport, backends.httpAddress)

	callGRPCBinThroughDataPlane(t, dialContext, backends.grpcH2CAddress, nil, trafficInspectionGRPCUnaryPath, 1)
	callGRPCBinThroughDataPlane(t, dialContext, backends.grpcH2CAddress, nil, trafficInspectionGRPCStreamPath, 10)
	grpcTLS := &tls.Config{
		MinVersion: tls.VersionTLS12,
		NextProtos: []string{"h2"},
		// The NodePort CONNECT authority is an IP while grpcbin uses a logical
		// SNI name. This client is scoped to verifying decryption and RAW GRPCS.
		InsecureSkipVerify: true, //nolint:gosec
	}
	callGRPCBinThroughDataPlane(t, dialContext, backends.grpcTLSAddress, grpcTLS, trafficInspectionGRPCUnaryPath, 1)
	callGRPCBinThroughDataPlane(t, dialContext, backends.grpcTLSAddress, grpcTLS, trafficInspectionGRPCStreamPath, 10)
	waitForTrafficInspectionEvents(t, output, map[string]bool{
		"http:request:request:" + trafficInspectionHTTPPath:        true,
		"http:body:response:" + trafficInspectionSSEPath:           true,
		"http:request:request:" + trafficInspectionWebSocketPath:   true,
		"http:response:response:" + trafficInspectionWebSocketPath: true,
		"grpc:body:request:" + trafficInspectionGRPCUnaryPath:      true,
		"grpc:body:response:" + trafficInspectionGRPCUnaryPath:     true,
		"grpc:body:request:" + trafficInspectionGRPCStreamPath:     true,
		"grpc:body:response:" + trafficInspectionGRPCStreamPath:    true,
		"grpcs:body:request:" + trafficInspectionGRPCUnaryPath:     true,
		"grpcs:body:response:" + trafficInspectionGRPCUnaryPath:    true,
		"grpcs:body:request:" + trafficInspectionGRPCStreamPath:    true,
		"grpcs:body:response:" + trafficInspectionGRPCStreamPath:   true,
	})
}

func callSSEThroughDataPlane(t *testing.T, transport *http.Transport, address string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(
		ctx, http.MethodGet, "http://"+address+trafficInspectionSSEPath, nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	response, err := (&http.Client{Transport: transport}).Do(request)
	if err != nil {
		t.Fatalf("call go-httpbin SSE through Data Plane: %v", err)
	}
	body, readErr := io.ReadAll(response.Body)
	closeErr := response.Body.Close()
	if readErr != nil || closeErr != nil {
		t.Fatal(readErr, closeErr)
	}
	if response.StatusCode != http.StatusOK ||
		!strings.HasPrefix(response.Header.Get("Content-Type"), "text/event-stream") ||
		bytes.Count(body, []byte("event: ping\n")) != 2 ||
		bytes.Count(body, []byte("data: ")) != 2 {
		t.Fatalf("unexpected go-httpbin SSE response: status=%d content-type=%q body=%q",
			response.StatusCode, response.Header.Get("Content-Type"), body)
	}
}

func callWebSocketThroughDataPlane(t *testing.T, transport *http.Transport, address string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	connection, response, err := websocket.Dial(
		ctx,
		"ws://"+address+trafficInspectionWebSocketPath,
		&websocket.DialOptions{HTTPClient: &http.Client{Transport: transport}},
	)
	if err != nil {
		t.Fatalf("connect go-httpbin WebSocket through Data Plane: %v", err)
	}
	defer connection.CloseNow()
	if response == nil || response.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("unexpected go-httpbin WebSocket upgrade response: %#v", response)
	}
	payload := []byte("kubeloop-websocket-" + uuid.NewString())
	if err := connection.Write(ctx, websocket.MessageText, payload); err != nil {
		t.Fatalf("write go-httpbin WebSocket payload: %v", err)
	}
	messageType, echoed, err := connection.Read(ctx)
	if err != nil {
		t.Fatalf("read go-httpbin WebSocket payload: %v", err)
	}
	if messageType != websocket.MessageText || !bytes.Equal(echoed, payload) {
		t.Fatalf("go-httpbin WebSocket echoed type=%v payload=%q, want type=%v payload=%q",
			messageType, echoed, websocket.MessageText, payload)
	}
}

func TestRealUnrecognizedTCPThroughDataPlane(t *testing.T) {
	harness.RequireE2E(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	kubeClient := kubeClient(t)
	if err := harness.EnsureEchoWorkload(ctx, kubeClient); err != nil {
		t.Fatalf("ensure traffic inspection namespace: %v", err)
	}
	nodeIP := trafficInspectionNodeIP(t, ctx, kubeClient)
	tcpEchoAddress := installTCPEchoBackend(t, ctx, kubeClient, nodeIP)
	dialContext, output := startTrafficInspectionDataPlane(t, ctx, kubeClient, nodeIP)

	callTCPEchoThroughDataPlane(t, dialContext, tcpEchoAddress, "in-cluster TCP echo")
	if events := output.Snapshot(); len(events) != 0 {
		t.Fatalf("unrecognized TCP traffic emitted inspection events: %#v", events)
	}
}

func startTrafficInspectionDataPlane(
	t *testing.T,
	ctx context.Context,
	kubeClient kubernetes.Interface,
	nodeIP string,
) (func(context.Context, string, string) (net.Conn, error), *trafficinspect.RingBufferSink) {
	t.Helper()
	_, identity, activeSession, remoteSession := trafficInspectionState(t, ctx, []string{nodeIP})
	gatewayIP := reachableHostIP(t, ctx, kubeClient)
	server, gatewayClient := startTrafficInspectionController(t, identity, activeSession, gatewayIP)
	t.Cleanup(server.Close)
	serverProfile := profile.Profile{ID: "traffic-inspection-e2e", BaseURL: server.URL}
	credentialStore := &e2eCredentialStore{
		profileID: serverProfile.ID,
		credential: credentials.Credential{
			TokenType: "Bearer", AccessToken: trafficInspectionAccessToken,
			AccessExpiresAt: identity.AccessExpiresAt, RefreshToken: "unused",
			RefreshExpiresAt: identity.AccessExpiresAt, DeviceID: identity.DeviceID,
		},
	}
	remoteClient, err := remote.New(credentialStore, e2eTokenRefresher{}, remote.Config{HTTPClient: gatewayClient})
	if err != nil {
		t.Fatal(err)
	}
	output, err := trafficinspect.NewRingBufferSink(256)
	if err != nil {
		t.Fatal(err)
	}
	protobufDecoder := trafficinspect.NewProtobufDecoder()
	if err := protobufDecoder.ReplaceSources(ctx, map[string]string{
		"grpcbin/types.proto":   trafficInspectionGRPCBinTypesProto,
		"grpcbin/service.proto": trafficInspectionGRPCBinServiceProto,
	}); err != nil {
		t.Fatalf("compile imported grpcbin protobuf schemas: %v", err)
	}
	dataPlane := startE2EDataPlaneWithInspection(
		t, ctx, remoteClient, gatewayClient, serverProfile, remoteSession,
		clientdataplane.TrafficInspectionConfig{
			Enabled: true, AuthorityPath: filepath.Join(t.TempDir(), "traffic-inspection-ca.pem"),
			TLSConfig: &tls.Config{ //nolint:gosec // grpcbin ships a legacy self-signed E2E certificate.
				MinVersion: tls.VersionTLS12, InsecureSkipVerify: true,
			},
			Sink:     output,
			Protobuf: protobufDecoder,
			Policy: trafficinspect.CapturePolicy{
				CaptureBodies: true,
				MaxBodyBytes:  4 << 20,
			},
		},
	)
	dialer, err := dataPlane.Dialer(serverProfile.ID)
	if err != nil {
		t.Fatal(err)
	}
	return dialer.DialContext, output
}

func callTCPEchoThroughDataPlane(
	t *testing.T,
	dialContext func(context.Context, string, string) (net.Conn, error),
	address string,
	backend string,
) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	connection, err := dialContext(ctx, "tcp", address)
	if err != nil {
		t.Fatalf("connect %s through Data Plane: %v", backend, err)
	}
	defer connection.Close()
	if err := connection.SetDeadline(time.Now().Add(20 * time.Second)); err != nil {
		t.Fatal(err)
	}
	payload := []byte("kubeloop-unknown-tcp-" + uuid.NewString() + "\n")
	if _, err := connection.Write(payload); err != nil {
		t.Fatalf("write %s payload: %v", backend, err)
	}
	echoed := make([]byte, len(payload))
	if _, err := io.ReadFull(connection, echoed); err != nil {
		t.Fatalf("read %s payload: %v", backend, err)
	}
	if !bytes.Equal(echoed, payload) {
		t.Fatalf("%s echoed payload = %q, want %q", backend, echoed, payload)
	}
}

func callGRPCBinThroughDataPlane(
	t *testing.T,
	dialContext func(context.Context, string, string) (net.Conn, error),
	address string,
	clientTLS *tls.Config,
	path string,
	wantFrames int,
) {
	t.Helper()
	transport := &http2.Transport{
		AllowHTTP: clientTLS == nil,
		DialTLSContext: func(ctx context.Context, network, target string, _ *tls.Config) (net.Conn, error) {
			connection, err := dialContext(ctx, network, target)
			if err != nil || clientTLS == nil {
				return connection, err
			}
			tlsConnection := tls.Client(connection, clientTLS.Clone())
			if err := tlsConnection.HandshakeContext(ctx); err != nil {
				_ = connection.Close()
				return nil, err
			}
			return tlsConnection, nil
		},
	}
	t.Cleanup(transport.CloseIdleConnections)
	scheme := "http"
	if clientTLS != nil {
		scheme = "https"
	}
	request, err := http.NewRequestWithContext(
		t.Context(), http.MethodPost, scheme+"://"+address+path,
		bytes.NewReader(complexGRPCBinFrame()),
	)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/grpc")
	request.Header.Set("TE", "trailers")
	response, err := (&http.Client{Transport: transport}).Do(request)
	if err != nil {
		t.Fatalf("call grpcbin %s through Data Plane: %v", address, err)
	}
	body, readErr := io.ReadAll(response.Body)
	closeErr := response.Body.Close()
	if readErr != nil || closeErr != nil {
		t.Fatal(readErr, closeErr)
	}
	frames, frameErr := countGRPCFrames(body)
	if response.StatusCode != http.StatusOK || response.Trailer.Get("Grpc-Status") != "0" ||
		frameErr != nil || frames != wantFrames {
		t.Fatalf("grpcbin %s%s response: status=%d frames=%d/%d frameErr=%v body=%x trailers=%v",
			address, path, response.StatusCode, frames, wantFrames, frameErr, body, response.Trailer)
	}
}

func complexGRPCBinFrame() []byte {
	payload := protowire.AppendTag(nil, 1, protowire.BytesType)
	payload = protowire.AppendString(payload, "kubeloop-complex")
	payload = protowire.AppendTag(payload, 2, protowire.BytesType)
	payload = protowire.AppendString(payload, "alpha")
	payload = protowire.AppendTag(payload, 2, protowire.BytesType)
	payload = protowire.AppendString(payload, "beta")
	payload = protowire.AppendTag(payload, 3, protowire.VarintType)
	payload = protowire.AppendVarint(payload, 42)
	packedInt32 := protowire.AppendVarint(nil, 7)
	packedInt32 = protowire.AppendVarint(packedInt32, 8)
	payload = protowire.AppendTag(payload, 4, protowire.BytesType)
	payload = protowire.AppendBytes(payload, packedInt32)
	payload = protowire.AppendTag(payload, 5, protowire.VarintType)
	payload = protowire.AppendVarint(payload, 2)
	packedEnums := protowire.AppendVarint(nil, 1)
	packedEnums = protowire.AppendVarint(packedEnums, 2)
	payload = protowire.AppendTag(payload, 6, protowire.BytesType)
	payload = protowire.AppendBytes(payload, packedEnums)
	nested := protowire.AppendTag(nil, 1, protowire.BytesType)
	nested = protowire.AppendString(nested, "nested")
	payload = protowire.AppendTag(payload, 7, protowire.BytesType)
	payload = protowire.AppendBytes(payload, nested)
	payload = protowire.AppendTag(payload, 9, protowire.VarintType)
	payload = protowire.AppendVarint(payload, 1)
	payload = protowire.AppendTag(payload, 11, protowire.VarintType)
	payload = protowire.AppendVarint(payload, 9_007_199_254_740_993)
	payload = protowire.AppendTag(payload, 13, protowire.BytesType)
	payload = protowire.AppendBytes(payload, []byte{0, 1, 2, 255})
	payload = protowire.AppendTag(payload, 15, protowire.Fixed32Type)
	payload = protowire.AppendFixed32(payload, math.Float32bits(3.5))
	mapEntry := protowire.AppendTag(nil, 1, protowire.BytesType)
	mapEntry = protowire.AppendString(mapEntry, "region")
	mapEntry = protowire.AppendTag(mapEntry, 2, protowire.VarintType)
	mapEntry = protowire.AppendVarint(mapEntry, 7)
	payload = protowire.AppendTag(payload, 17, protowire.BytesType)
	payload = protowire.AppendBytes(payload, mapEntry)
	payload = protowire.AppendTag(payload, 18, protowire.BytesType)
	payload = protowire.AppendString(payload, "selected")
	timestamp := protowire.AppendTag(nil, 1, protowire.VarintType)
	timestamp = protowire.AppendVarint(timestamp, 1_700_000_000)
	payload = protowire.AppendTag(payload, 19, protowire.BytesType)
	payload = protowire.AppendBytes(payload, timestamp)

	frame := make([]byte, 5, len(payload)+5)
	binary.BigEndian.PutUint32(frame[1:], uint32(len(payload)))
	return append(frame, payload...)
}

func countGRPCFrames(payload []byte) (int, error) {
	frames := 0
	for len(payload) > 0 {
		if len(payload) < 5 {
			return frames, io.ErrUnexpectedEOF
		}
		if payload[0] > 1 {
			return frames, fmt.Errorf("invalid gRPC compressed flag %d", payload[0])
		}
		frameSize := int(binary.BigEndian.Uint32(payload[1:5]))
		if frameSize > len(payload)-5 {
			return frames, io.ErrUnexpectedEOF
		}
		payload = payload[5+frameSize:]
		frames++
	}
	return frames, nil
}

func waitForTrafficInspectionEvents(t *testing.T, sink *trafficinspect.RingBufferSink, expected map[string]bool) {
	t.Helper()
	err := wait.PollUntilContextTimeout(t.Context(), 50*time.Millisecond, 10*time.Second, true, func(context.Context) (bool, error) {
		seen := make(map[string]bool)
		for _, event := range sink.Snapshot() {
			if event.Raw == nil || event.Raw.Data == "" {
				continue
			}
			raw, err := base64.StdEncoding.DecodeString(event.Raw.Data)
			if err != nil {
				return false, err
			}
			path := ""
			if event.HTTP != nil {
				path = event.HTTP.Path
			}
			key := string(event.Protocol) + ":" + string(event.Type) + ":" + event.Raw.Direction + ":" + path
			if event.Raw.Format == "grpc" {
				frames, err := countGRPCFrames(raw)
				if err != nil {
					t.Fatalf("invalid RAW gRPC frames for %s: %v payload=%x", key, err, raw)
				}
				wantFrames := 1
				if path == trafficInspectionGRPCStreamPath && event.Raw.Direction == "response" {
					wantFrames = 10
				}
				if frames != wantFrames {
					t.Fatalf("RAW gRPC frame count for %s = %d, want %d", key, frames, wantFrames)
				}
				assertDecodedGRPCBinProtobuf(t, event, wantFrames)
			}
			seen[key] = true
		}
		for key := range expected {
			if !seen[key] {
				return false, nil
			}
		}
		return true, nil
	})
	if err != nil {
		t.Fatalf("traffic inspection events incomplete: %v events=%#v", err, sink.Snapshot())
	}
}

func assertDecodedGRPCBinProtobuf(t *testing.T, event trafficinspect.Event, wantMessages int) {
	t.Helper()
	if event.Protobuf == nil {
		t.Fatalf("protobuf decode is missing for %s %s", event.Raw.Direction, event.GRPC.Path)
	}
	if event.Protobuf.Error != "" || event.Protobuf.Schema != "proto" ||
		event.Protobuf.MessageType != "grpcbin.DummyMessage" {
		t.Fatalf("protobuf decode metadata = %#v", event.Protobuf)
	}
	var messages []map[string]any
	if err := json.Unmarshal([]byte(event.Protobuf.Data), &messages); err != nil {
		t.Fatalf("decode protobuf JSON: %v data=%s", err, event.Protobuf.Data)
	}
	if len(messages) != wantMessages {
		t.Fatalf("decoded protobuf messages = %d, want %d", len(messages), wantMessages)
	}
	for index, message := range messages {
		if message["f_string"] != "kubeloop-complex" || message["f_enum"] != "ENUM_2" {
			t.Fatalf("decoded protobuf message %d lacks grpcbin fields: %#v", index, message)
		}
	}
	if event.Raw.Direction != "request" {
		return
	}
	message := messages[0]
	if message["f_choice_text"] != "selected" {
		t.Fatalf("decoded protobuf oneof = %#v", message["f_choice_text"])
	}
	decodedMap, ok := message["f_map"].(map[string]any)
	if !ok || decodedMap["region"] != float64(7) {
		t.Fatalf("decoded protobuf map = %#v", message["f_map"])
	}
	if message["f_timestamp"] != "2023-11-14T22:13:20Z" {
		t.Fatalf("decoded protobuf timestamp = %#v", message["f_timestamp"])
	}
}

func trafficInspectionState(
	t *testing.T,
	ctx context.Context,
	serviceIPs []string,
) (*storage.Store, controlplaneapi.Identity, sessionapi.ActiveSession, remote.Session) {
	t.Helper()
	stateStore, err := storage.Open(ctx, storage.Config{
		Backend: storage.BackendSQLite, SQLitePath: filepath.Join(t.TempDir(), "traffic-inspection.db"),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = stateStore.Close() })
	now := time.Now().UTC()
	identityID, authorizationID, sessionID := uuid.NewString(), uuid.NewString(), uuid.NewString()
	deviceID := "traffic-inspection-e2e-device"
	if _, err := stateStore.Identities().Create(ctx, storage.Identity{
		ID: identityID, Type: "human", DisplayName: "Traffic Inspection E2E", Status: "active",
		CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	expiresAt := now.Add(10 * time.Minute)
	createOAuthGrant(t, ctx, stateStore, authorizationID, identityID, deviceID, 9, now, expiresAt)
	network, err := networkspec.Normalize(networkspec.Spec{ServiceIPs: serviceIPs})
	if err != nil {
		t.Fatal(err)
	}
	networkJSON, _ := networkspec.CanonicalJSON(network)
	networkHash, _ := networkspec.Hash(network)
	if err := stateStore.Sessions().Create(ctx, storage.Session{
		ID: sessionID, IdentityID: identityID, DeviceID: deviceID, ClusterID: "minikube",
		Namespace: harness.EchoNamespace, State: "active", Generation: 1,
		NetworkSpec: networkJSON, NetworkSpecHash: networkHash,
		CreatedAt: now, UpdatedAt: now, LastHeartbeatAt: now, ExpiresAt: expiresAt,
	}); err != nil {
		t.Fatal(err)
	}
	identity := controlplaneapi.Identity{
		Subject: identityID, DeviceID: deviceID, AuthorizationID: authorizationID, AccessExpiresAt: expiresAt,
	}
	active := sessionapi.ActiveSession{
		ID: sessionID, Namespace: harness.EchoNamespace, Generation: 1,
		ExpiresAt: expiresAt, NetworkSpecHash: networkHash,
	}
	clientSession := remote.Session{
		ID: sessionID, Namespace: harness.EchoNamespace, State: "active", Generation: 1,
		CreatedAt: now, UpdatedAt: now, LastHeartbeatAt: now, ExpiresAt: expiresAt,
		NetworkSpec: network, NetworkSpecHash: networkHash,
	}
	return stateStore, identity, active, clientSession
}

func startTrafficInspectionController(
	t *testing.T,
	identity controlplaneapi.Identity,
	session sessionapi.ActiveSession,
	gatewayIP string,
) (*httptest.Server, *http.Client) {
	t.Helper()
	gateway := startE2ETrafficGateway(t, gatewayIP, nil, nil)
	server, err := controlplane.NewServer(
		controlplane.Config{PublicURL: "http://127.0.0.1"}, controlplane.BuildInfo{},
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		controlplane.WithAuthenticator(controlplaneapi.AuthenticatorFunc(func(request *http.Request) (controlplaneapi.Identity, *controlplaneapi.Error) {
			if request.Header.Get("Authorization") != "Bearer "+trafficInspectionAccessToken {
				return controlplaneapi.Identity{}, &controlplaneapi.Error{Code: controlplaneapi.CodeUnauthenticated, Message: "invalid e2e access token"}
			}
			return identity, nil
		})),
		controlplane.WithAuthorizer(authorization.NewAuthenticated()),
		controlplane.WithAPIRoutes(controlplane.APIRoutes{
			Tickets: ticketapi.NewRoutes(gateway.tickets, e2eExecSessionValidator{
				identityID: identity.Subject, session: session,
			}).Endpoints(),
		}),
	)
	if err != nil {
		t.Fatal(err)
	}
	return startE2EControlPlaneServer(t, server.Handler(), gateway)
}

type trafficInspectionBackends struct {
	nodeIP         string
	httpAddress    string
	grpcH2CAddress string
	grpcTLSAddress string
}

func installTrafficInspectionBackends(
	t *testing.T,
	ctx context.Context,
	client kubernetes.Interface,
) trafficInspectionBackends {
	t.Helper()
	suffix := strings.ToLower(uuid.NewString()[:8])
	httpName := "traffic-httpbin-" + suffix
	grpcName := "traffic-grpcbin-" + suffix
	httpImage := strings.TrimSpace(os.Getenv("KUBELOOP_HTTPBIN_IMAGE"))
	if httpImage == "" {
		httpImage = "ghcr.io/mccutchen/go-httpbin:latest"
	}
	grpcImage := strings.TrimSpace(os.Getenv("KUBELOOP_GRPCBIN_IMAGE"))
	if grpcImage == "" {
		grpcImage = "docker.io/kong/grpcbin:latest"
	}
	httpService := installTrafficInspectionBackend(t, ctx, client, httpName, httpImage, nil, []corev1.ContainerPort{
		{Name: "http", ContainerPort: 8080},
	}, []corev1.ServicePort{
		{Name: "http", Port: 8080, TargetPort: intstr.FromString("http")},
	}, &corev1.Probe{ProbeHandler: corev1.ProbeHandler{HTTPGet: &corev1.HTTPGetAction{
		Path: "/get", Port: intstr.FromString("http"),
	}}, PeriodSeconds: 1, FailureThreshold: 60})
	grpcService := installTrafficInspectionBackend(t, ctx, client, grpcName, grpcImage, nil, []corev1.ContainerPort{
		{Name: "grpc-h2c", ContainerPort: 9000},
		{Name: "grpc-tls", ContainerPort: 9001},
	}, []corev1.ServicePort{
		{Name: "grpc-h2c", Port: 9000, TargetPort: intstr.FromString("grpc-h2c")},
		{Name: "grpc-tls", Port: 9001, TargetPort: intstr.FromString("grpc-tls")},
	}, &corev1.Probe{ProbeHandler: corev1.ProbeHandler{TCPSocket: &corev1.TCPSocketAction{
		Port: intstr.FromString("grpc-h2c"),
	}}, PeriodSeconds: 1, FailureThreshold: 60})
	nodeIP := trafficInspectionNodeIP(t, ctx, client)
	return trafficInspectionBackends{
		nodeIP:         nodeIP,
		httpAddress:    net.JoinHostPort(nodeIP, serviceNodePort(t, httpService, "http")),
		grpcH2CAddress: net.JoinHostPort(nodeIP, serviceNodePort(t, grpcService, "grpc-h2c")),
		grpcTLSAddress: net.JoinHostPort(nodeIP, serviceNodePort(t, grpcService, "grpc-tls")),
	}
}

func trafficInspectionNodeIP(t *testing.T, ctx context.Context, client kubernetes.Interface) string {
	t.Helper()
	nodes, err := client.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil || len(nodes.Items) == 0 {
		t.Fatalf("list traffic inspection E2E nodes: %v", err)
	}
	nodeIP := ""
	for _, address := range nodes.Items[0].Status.Addresses {
		if address.Type == corev1.NodeInternalIP {
			nodeIP = address.Address
			break
		}
	}
	if net.ParseIP(nodeIP) == nil {
		t.Fatalf("traffic inspection E2E node IP is invalid: %q", nodeIP)
	}
	return nodeIP
}

func installTCPEchoBackend(
	t *testing.T,
	ctx context.Context,
	client kubernetes.Interface,
	nodeIP string,
) string {
	t.Helper()
	image := strings.TrimSpace(os.Getenv("KUBELOOP_TCP_ECHO_IMAGE"))
	if image == "" {
		image = "docker.io/library/python:3.12-alpine"
	}
	const server = "" +
		"import socket\n" +
		"s=socket.socket()\n" +
		"s.setsockopt(socket.SOL_SOCKET,socket.SO_REUSEADDR,1)\n" +
		"s.bind(('0.0.0.0',30000))\n" +
		"s.listen()\n" +
		"while True:\n" +
		" c,_=s.accept()\n" +
		" while data:=c.recv(65536): c.sendall(data)\n" +
		" c.close()\n"
	return installTCPEchoService(t, ctx, client, nodeIP, image, []string{"python", "-c", server})
}

func installTCPEchoService(
	t *testing.T,
	ctx context.Context,
	client kubernetes.Interface,
	nodeIP, image string,
	args []string,
) string {
	t.Helper()
	name := "traffic-tcp-echo-" + strings.ToLower(uuid.NewString()[:8])
	service := installTrafficInspectionBackend(
		t, ctx, client, name, image,
		args,
		[]corev1.ContainerPort{{Name: "tcp-echo", ContainerPort: 30000}},
		[]corev1.ServicePort{{Name: "tcp-echo", Port: 30000, TargetPort: intstr.FromString("tcp-echo")}},
		&corev1.Probe{ProbeHandler: corev1.ProbeHandler{TCPSocket: &corev1.TCPSocketAction{
			Port: intstr.FromString("tcp-echo"),
		}}, PeriodSeconds: 1, FailureThreshold: 60},
	)
	return net.JoinHostPort(nodeIP, serviceNodePort(t, service, "tcp-echo"))
}

func serviceNodePort(t *testing.T, service *corev1.Service, name string) string {
	t.Helper()
	for _, port := range service.Spec.Ports {
		if port.Name == name && port.NodePort > 0 {
			return strconv.Itoa(int(port.NodePort))
		}
	}
	t.Fatalf("Service %s has no NodePort named %s", service.Name, name)
	return ""
}

func installTrafficInspectionBackend(
	t *testing.T,
	ctx context.Context,
	client kubernetes.Interface,
	name, image string,
	args []string,
	containerPorts []corev1.ContainerPort,
	servicePorts []corev1.ServicePort,
	readiness *corev1.Probe,
) *corev1.Service {
	t.Helper()
	labels := map[string]string{"app.kubernetes.io/name": name, "app.kubernetes.io/component": "traffic-inspection-e2e"}
	_, err := client.AppsV1().Deployments(harness.EchoNamespace).Create(ctx, &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: name, Labels: labels},
		Spec: appsv1.DeploymentSpec{
			Replicas: ptr.To[int32](1), Selector: &metav1.LabelSelector{MatchLabels: labels},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: labels},
				Spec: corev1.PodSpec{
					AutomountServiceAccountToken: ptr.To(false),
					Containers: []corev1.Container{{
						Name: name, Image: image, ImagePullPolicy: corev1.PullIfNotPresent,
						Args: args, Ports: containerPorts, ReadinessProbe: readiness,
					}},
				},
			},
		},
	}, metav1.CreateOptions{})
	if err != nil {
		t.Fatalf("create %s Deployment: %v", name, err)
	}
	service, err := client.CoreV1().Services(harness.EchoNamespace).Create(ctx, &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: name, Labels: labels},
		Spec:       corev1.ServiceSpec{Type: corev1.ServiceTypeNodePort, Selector: labels, Ports: servicePorts},
	}, metav1.CreateOptions{})
	if err != nil {
		t.Fatalf("create %s Service: %v", name, err)
	}
	t.Cleanup(func() {
		cleanupContext, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = client.AppsV1().Deployments(harness.EchoNamespace).Delete(cleanupContext, name, metav1.DeleteOptions{})
		_ = client.CoreV1().Services(harness.EchoNamespace).Delete(cleanupContext, name, metav1.DeleteOptions{})
	})
	if err := wait.PollUntilContextTimeout(ctx, time.Second, 2*time.Minute, true, func(ctx context.Context) (bool, error) {
		deployment, err := client.AppsV1().Deployments(harness.EchoNamespace).Get(ctx, name, metav1.GetOptions{})
		return err == nil && deployment.Status.AvailableReplicas > 0, err
	}); err != nil {
		t.Fatalf("wait for %s Deployment: %v", name, err)
	}
	return service
}
