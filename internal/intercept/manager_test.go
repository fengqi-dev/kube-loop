package intercept

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/fengqi-dev/kube-loop/internal/gateway"
)

type fakeCluster struct {
	service          *corev1.Service
	applied          *ServiceInterceptRequest
	restored         bool
	preview          *PreviewServiceRequest
	deleted          bool
	previewIP        string
	endpointsSubsets []corev1.EndpointSubset
	restoreErr       error
	deleteErr        error
	restoreCalls     int
	deleteCalls      int
}

func (f *fakeCluster) GetService(
	context.Context, string, string, string,
) (*Service, error) {
	service := f.service.DeepCopy()
	ports := make([]ServicePort, 0, len(service.Spec.Ports))
	for _, port := range service.Spec.Ports {
		ports = append(ports, ServicePort{
			Name: port.Name, Protocol: normalizeProtocol(string(port.Protocol)), Port: port.Port,
		})
	}
	return &Service{
		Namespace: service.Namespace,
		Name:      service.Name,
		ClusterIP: service.Spec.ClusterIP,
		Selector:  service.Spec.Selector,
		Ports:     ports,
	}, nil
}

func (f *fakeCluster) ApplyServiceIntercept(
	_ context.Context, _ string, request ServiceInterceptRequest,
) (Lease, []Backend, error) {
	copyRequest := request
	f.applied = &copyRequest
	return fakeLease{release: f.restore}, endpointSubsetModels(f.endpointsSubsets), nil
}

func (f *fakeCluster) restore(context.Context) error {
	f.restoreCalls++
	if f.restoreErr != nil {
		return f.restoreErr
	}
	f.restored = true
	return nil
}

func (f *fakeCluster) CreatePreviewService(
	_ context.Context, _ string, request PreviewServiceRequest,
) (*Service, Lease, error) {
	copyRequest := request
	f.preview = &copyRequest
	ip := f.previewIP
	if ip == "" {
		ip = "10.96.9.9"
	}
	return &Service{
		Name: request.Service, Namespace: request.Namespace, ClusterIP: ip,
		Ports: []ServicePort{{Port: request.Ports[0].ServicePort}},
	}, fakeLease{release: f.delete}, nil
}

func (f *fakeCluster) delete(context.Context) error {
	f.deleteCalls++
	if f.deleteErr != nil {
		return f.deleteErr
	}
	f.deleted = true
	return nil
}

type fakeLease struct {
	release func(context.Context) error
}

func (l fakeLease) Release(ctx context.Context) error {
	return l.release(ctx)
}

func endpointSubsetModels(subsets []corev1.EndpointSubset) []Backend {
	var backends []Backend
	for _, subset := range subsets {
		ports := make([]BackendPort, 0, len(subset.Ports))
		for _, port := range subset.Ports {
			ports = append(ports, BackendPort{
				Name: port.Name, Protocol: normalizeProtocol(string(port.Protocol)), Port: port.Port,
			})
		}
		for _, address := range subset.Addresses {
			backends = append(backends, Backend{Address: address.IP, Ports: ports})
		}
	}
	return backends
}

func TestStartStopInterceptRegistersAndRestores(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	server := gateway.NewServer(log.New(io.Discard, "", 0), time.Second)
	go func() { _ = server.Serve(listener) }()

	local, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer local.Close()
	go func() {
		conn, err := local.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		buf := make([]byte, 32)
		n, _ := conn.Read(buf)
		_, _ = fmt.Fprintf(conn, "local:%s", buf[:n])
	}()

	api := &fakeCluster{
		service: &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{Name: "api", Namespace: "default"},
			Spec: corev1.ServiceSpec{
				ClusterIP: "10.96.1.10",
				Selector:  map[string]string{"app": "api"},
				Ports: []corev1.ServicePort{{
					Name: "http", Port: 80, Protocol: corev1.ProtocolTCP,
				}},
			},
		},
	}
	manager := NewManager(api)
	ctx := context.Background()
	if err := manager.Start(ctx, "minikube", "10.244.0.8", listener.Addr().String()); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = manager.StopAll(context.Background()) }()

	info, err := manager.StartIntercept(ctx, Mapping{
		Namespace: "default",
		Service:   "api",
		Ports: []PortMapping{{
			ServicePort: 80, Protocol: "TCP",
			LocalHost: "127.0.0.1", LocalPort: local.Addr().(*net.TCPAddr).Port,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if api.applied == nil || api.applied.GatewayIP != "10.244.0.8" {
		t.Fatalf("apply not called: %#v", api.applied)
	}
	if len(manager.List()) != 1 {
		t.Fatalf("list=%d", len(manager.List()))
	}

	listenPort := info.Ports[0].ListenPort
	client, err := net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", listenPort))
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if _, err := client.Write([]byte("ping")); err != nil {
		t.Fatal(err)
	}
	_ = client.SetReadDeadline(time.Now().Add(3 * time.Second))
	buf := make([]byte, 64)
	n, err := client.Read(buf)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(buf[:n]); got != "local:ping" {
		t.Fatalf("got %q", got)
	}

	if err := manager.Stop(ctx, info.ID); err != nil {
		t.Fatal(err)
	}
	if !api.restored {
		t.Fatal("restore not called")
	}
}

func TestRecoverControlAtUsesReplacementGateway(t *testing.T) {
	first, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	firstServer := gateway.NewServer(log.New(io.Discard, "", 0), time.Second)
	go func() { _ = firstServer.Serve(first) }()

	second, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	secondServer := gateway.NewServer(log.New(io.Discard, "", 0), time.Second)
	go func() { _ = secondServer.Serve(second) }()

	manager := NewManager(&fakeCluster{})
	if err := manager.Start(
		context.Background(), "minikube", "10.244.0.8", first.Addr().String(),
	); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = manager.StopAll(context.Background()) }()

	if err := manager.RecoverControlAt(
		context.Background(), "10.244.0.9", second.Addr().String(),
	); err != nil {
		t.Fatal(err)
	}
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if manager.gatewayIP != "10.244.0.9" ||
		manager.gatewayAddress != second.Addr().String() ||
		!manager.control.ready() {
		t.Fatalf(
			"replacement gateway not active: ip=%q address=%q ready=%v",
			manager.gatewayIP, manager.gatewayAddress, manager.control.ready(),
		)
	}
}

func TestStartMirrorTeesToLocalKeepsPrimaryResponse(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	server := gateway.NewServer(log.New(io.Discard, "", 0), time.Second)
	go func() { _ = server.Serve(listener) }()

	primary, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer primary.Close()
	go func() {
		conn, err := primary.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		buf := make([]byte, 32)
		n, _ := conn.Read(buf)
		_, _ = fmt.Fprintf(conn, "primary:%s", buf[:n])
	}()

	mirrored := make(chan string, 1)
	local, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer local.Close()
	go func() {
		conn, err := local.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		buf := make([]byte, 32)
		n, _ := conn.Read(buf)
		mirrored <- string(buf[:n])
		_, _ = conn.Write([]byte("local-should-be-ignored"))
	}()

	primaryPort := primary.Addr().(*net.TCPAddr).Port
	api := &fakeCluster{
		service: &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{Name: "api", Namespace: "default"},
			Spec: corev1.ServiceSpec{
				ClusterIP: "10.96.1.10",
				Selector:  map[string]string{"app": "api"},
				Ports: []corev1.ServicePort{{
					Name: "http", Port: 80, Protocol: corev1.ProtocolTCP,
				}},
			},
		},
		endpointsSubsets: []corev1.EndpointSubset{{
			Addresses: []corev1.EndpointAddress{{IP: "127.0.0.1"}},
			Ports: []corev1.EndpointPort{{
				Name: "http", Port: int32(primaryPort), Protocol: corev1.ProtocolTCP,
			}},
		}},
	}
	manager := NewManager(api)
	ctx := context.Background()
	if err := manager.Start(ctx, "minikube", "10.244.0.8", listener.Addr().String()); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = manager.StopAll(context.Background()) }()

	info, err := manager.StartMirror(ctx, Mapping{
		Namespace: "default",
		Service:   "api",
		Ports: []PortMapping{{
			ServicePort: 80, Protocol: "TCP",
			LocalHost: "127.0.0.1", LocalPort: local.Addr().(*net.TCPAddr).Port,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode != ModeMirror {
		t.Fatalf("mode=%q", info.Mode)
	}
	if len(manager.List()) != 0 || len(manager.ListMirrors()) != 1 {
		t.Fatalf("list exchange=%d mirror=%d", len(manager.List()), len(manager.ListMirrors()))
	}

	client, err := net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", info.Ports[0].ListenPort))
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if _, err := client.Write([]byte("ping")); err != nil {
		t.Fatal(err)
	}
	_ = client.SetReadDeadline(time.Now().Add(3 * time.Second))
	buf := make([]byte, 64)
	n, err := client.Read(buf)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(buf[:n]); got != "primary:ping" {
		t.Fatalf("client got %q, want primary response", got)
	}
	select {
	case got := <-mirrored:
		if got != "ping" {
			t.Fatalf("mirror got %q", got)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("local mirror did not receive request copy")
	}
}

func TestStartMirrorUDPTeesToLocalKeepsPrimaryResponse(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	server := gateway.NewServer(log.New(io.Discard, "", 0), time.Second)
	go func() { _ = server.Serve(listener) }()

	primary, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer primary.Close()
	go func() {
		buf := make([]byte, 32)
		n, addr, err := primary.ReadFrom(buf)
		if err != nil {
			return
		}
		_, _ = primary.WriteTo(fmt.Appendf(nil, "primary:%s", buf[:n]), addr)
	}()

	mirrored := make(chan string, 1)
	local, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer local.Close()
	go func() {
		buf := make([]byte, 32)
		n, addr, err := local.ReadFrom(buf)
		if err != nil {
			return
		}
		mirrored <- string(buf[:n])
		_, _ = local.WriteTo([]byte("local-should-be-ignored"), addr)
	}()

	primaryPort := primary.LocalAddr().(*net.UDPAddr).Port
	api := &fakeCluster{
		service: &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{Name: "dns", Namespace: "default"},
			Spec: corev1.ServiceSpec{
				ClusterIP: "10.96.1.53",
				Selector:  map[string]string{"app": "dns"},
				Ports: []corev1.ServicePort{{
					Name: "dns", Port: 53, Protocol: corev1.ProtocolUDP,
				}},
			},
		},
		endpointsSubsets: []corev1.EndpointSubset{{
			Addresses: []corev1.EndpointAddress{{IP: "127.0.0.1"}},
			Ports: []corev1.EndpointPort{{
				Name: "dns", Port: int32(primaryPort), Protocol: corev1.ProtocolUDP,
			}},
		}},
	}
	manager := NewManager(api)
	ctx := context.Background()
	if err := manager.Start(ctx, "minikube", "10.244.0.8", listener.Addr().String()); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = manager.StopAll(context.Background()) }()

	info, err := manager.StartMirror(ctx, Mapping{
		Namespace: "default",
		Service:   "dns",
		Ports: []PortMapping{{
			ServicePort: 53, Protocol: "UDP",
			LocalHost: "127.0.0.1", LocalPort: local.LocalAddr().(*net.UDPAddr).Port,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode != ModeMirror {
		t.Fatalf("mode=%q", info.Mode)
	}

	client, err := net.DialUDP("udp", nil, &net.UDPAddr{
		IP: net.ParseIP("127.0.0.1"), Port: int(info.Ports[0].ListenPort),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if _, err := client.Write([]byte("ping")); err != nil {
		t.Fatal(err)
	}
	_ = client.SetReadDeadline(time.Now().Add(3 * time.Second))
	buf := make([]byte, 64)
	n, err := client.Read(buf)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(buf[:n]); got != "primary:ping" {
		t.Fatalf("client got %q, want primary response", got)
	}
	select {
	case got := <-mirrored:
		if got != "ping" {
			t.Fatalf("mirror got %q", got)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("local mirror did not receive request copy")
	}
}

func TestStartStopPreviewCreatesAndDeletes(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	server := gateway.NewServer(log.New(io.Discard, "", 0), time.Second)
	go func() { _ = server.Serve(listener) }()

	local, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer local.Close()

	api := &fakeCluster{}
	manager := NewManager(api)
	ctx := context.Background()
	if err := manager.Start(ctx, "minikube", "10.244.0.8", listener.Addr().String()); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = manager.StopAll(context.Background()) }()

	info, err := manager.StartPreview(ctx, PreviewRequest{
		Namespace: "demo",
		Name:      "local-api",
		Ports: []PortMapping{{
			ServicePort: 8080, Protocol: "TCP",
			LocalHost: "127.0.0.1", LocalPort: local.Addr().(*net.TCPAddr).Port,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !info.Preview || info.ClusterIP == "" {
		t.Fatalf("preview info = %#v", info)
	}
	if api.preview == nil || api.preview.Service != "local-api" {
		t.Fatalf("create not called: %#v", api.preview)
	}
	if len(manager.List()) != 0 || len(manager.ListPreviews()) != 1 {
		t.Fatalf("list intercept=%d preview=%d", len(manager.List()), len(manager.ListPreviews()))
	}
	if err := manager.Stop(ctx, info.ID); err != nil {
		t.Fatal(err)
	}
	if !api.deleted {
		t.Fatal("delete not called")
	}
}

func TestExchangeAndMirrorAreMutuallyExclusive(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	server := gateway.NewServer(log.New(io.Discard, "", 0), time.Second)
	go func() { _ = server.Serve(listener) }()

	local, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer local.Close()

	api := &fakeCluster{
		service: &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{Name: "api", Namespace: "default"},
			Spec: corev1.ServiceSpec{
				ClusterIP: "10.96.1.10",
				Selector:  map[string]string{"app": "api"},
				Ports: []corev1.ServicePort{{
					Name: "http", Port: 80, Protocol: corev1.ProtocolTCP,
				}},
			},
		},
		endpointsSubsets: []corev1.EndpointSubset{{
			Addresses: []corev1.EndpointAddress{{IP: "10.244.0.10"}},
			Ports:     []corev1.EndpointPort{{Name: "http", Port: 80, Protocol: corev1.ProtocolTCP}},
		}},
	}
	manager := NewManager(api)
	ctx := context.Background()
	if err := manager.Start(ctx, "minikube", "10.244.0.8", listener.Addr().String()); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = manager.StopAll(context.Background()) }()

	mapping := Mapping{
		Namespace: "default",
		Service:   "api",
		Ports: []PortMapping{{
			ServicePort: 80, Protocol: "TCP",
			LocalHost: "127.0.0.1", LocalPort: local.Addr().(*net.TCPAddr).Port,
		}},
	}
	if _, err := manager.StartIntercept(ctx, mapping); err != nil {
		t.Fatal(err)
	}
	_, err = manager.StartMirror(ctx, mapping)
	if err == nil {
		t.Fatal("expected mirror to be rejected while exchange is active")
	}
	if !strings.Contains(err.Error(), "mutually exclusive") {
		t.Fatalf("got %v", err)
	}
}

func TestServiceRewriteHostsIncludesClusterIPAndDNS(t *testing.T) {
	hosts := serviceRewriteHosts(&Service{
		Name: "static-web", Namespace: "default", ClusterIP: "10.105.153.132",
	})
	joined := strings.Join(hosts, ",")
	for _, want := range []string{
		"10.105.153.132",
		"static-web.default.svc.cluster.local",
		"static-web.default.svc",
		"static-web.default",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("hosts %v missing %q", hosts, want)
		}
	}
}

func TestHostTCPServesExchangeWithoutGatewayHairpin(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	server := gateway.NewServer(log.New(io.Discard, "", 0), time.Second)
	go func() { _ = server.Serve(listener) }()

	local, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer local.Close()
	go func() {
		conn, err := local.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		buf := make([]byte, 64)
		n, _ := conn.Read(buf)
		_, _ = fmt.Fprintf(conn, "local:%s", buf[:n])
	}()

	api := &fakeCluster{
		service: &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{Name: "static-web", Namespace: "default"},
			Spec: corev1.ServiceSpec{
				ClusterIP: "10.105.153.132",
				Selector:  map[string]string{"role": "myrole"},
				Ports: []corev1.ServicePort{{
					Name: "web", Port: 80, Protocol: corev1.ProtocolTCP,
				}},
			},
		},
	}
	manager := NewManager(api)
	ctx := context.Background()
	if err := manager.Start(ctx, "minikube", "10.244.0.199", listener.Addr().String()); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = manager.StopAll(context.Background()) }()

	info, err := manager.StartIntercept(ctx, Mapping{
		Namespace: "default",
		Service:   "static-web",
		Ports: []PortMapping{{
			ServicePort: 80, Protocol: "TCP",
			LocalHost: "127.0.0.1", LocalPort: local.Addr().(*net.TCPAddr).Port,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}

	serve, ok := manager.HostTCP("10.105.153.132", 80)
	if !ok || serve == nil {
		t.Fatal("expected host TCP route for ClusterIP")
	}
	serveDNS, ok := manager.HostTCP("static-web.default.svc.cluster.local", 80)
	if !ok || serveDNS == nil {
		t.Fatal("expected host TCP route for DNS name")
	}

	left, right := net.Pipe()
	defer left.Close()
	go serve(right)
	if _, err := left.Write([]byte("ping")); err != nil {
		t.Fatal(err)
	}
	_ = left.SetReadDeadline(time.Now().Add(3 * time.Second))
	buf := make([]byte, 64)
	n, err := left.Read(buf)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(buf[:n]); got != "local:ping" {
		t.Fatalf("got %q", got)
	}
	if err := manager.Stop(ctx, info.ID); err != nil {
		t.Fatal(err)
	}
	if _, ok := manager.HostTCP("10.105.153.132", 80); ok {
		t.Fatal("host route should be cleared after stop")
	}
}

func TestHostUDPServesExchangeWithoutGatewayHairpin(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	server := gateway.NewServer(log.New(io.Discard, "", 0), time.Second)
	go func() { _ = server.Serve(listener) }()

	local, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer local.Close()
	go func() {
		buf := make([]byte, 64)
		for {
			n, addr, err := local.ReadFrom(buf)
			if err != nil {
				return
			}
			_, _ = local.WriteTo(fmt.Appendf(nil, "local-udp:%s", buf[:n]), addr)
		}
	}()

	api := &fakeCluster{
		service: &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{Name: "static-web", Namespace: "default"},
			Spec: corev1.ServiceSpec{
				ClusterIP: "10.105.153.132",
				Selector:  map[string]string{"role": "myrole"},
				Ports: []corev1.ServicePort{{
					Name: "dns", Port: 9090, Protocol: corev1.ProtocolUDP,
				}},
			},
		},
	}
	manager := NewManager(api)
	ctx := context.Background()
	if err := manager.Start(ctx, "minikube", "10.244.0.199", listener.Addr().String()); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = manager.StopAll(context.Background()) }()

	info, err := manager.StartIntercept(ctx, Mapping{
		Namespace: "default",
		Service:   "static-web",
		Ports: []PortMapping{{
			ServicePort: 9090, Protocol: "UDP",
			LocalHost: "127.0.0.1", LocalPort: local.LocalAddr().(*net.UDPAddr).Port,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}

	dial, ok := manager.HostUDP("10.105.153.132", 9090)
	if !ok || dial == nil {
		t.Fatal("expected host UDP route for ClusterIP")
	}
	conn, err := dial(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
	if _, err := conn.Write([]byte("ping")); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 64)
	n, err := conn.Read(buf)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(buf[:n]); got != "local-udp:ping" {
		t.Fatalf("got %q", got)
	}
	if err := manager.Stop(ctx, info.ID); err != nil {
		t.Fatal(err)
	}
	if _, ok := manager.HostUDP("10.105.153.132", 9090); ok {
		t.Fatal("host UDP route should be cleared after stop")
	}
}

func TestRecoverControlRedialsAndReregisters(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	server := gateway.NewServer(log.New(io.Discard, "", 0), time.Second)
	go func() { _ = server.Serve(listener) }()

	local, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer local.Close()
	go func() {
		for {
			conn, err := local.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				buf := make([]byte, 32)
				n, _ := c.Read(buf)
				_, _ = fmt.Fprintf(c, "local:%s", buf[:n])
			}(conn)
		}
	}()

	api := &fakeCluster{
		service: &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{Name: "api", Namespace: "default"},
			Spec: corev1.ServiceSpec{
				ClusterIP: "10.96.1.10",
				Selector:  map[string]string{"app": "api"},
				Ports: []corev1.ServicePort{{
					Name: "http", Port: 80, Protocol: corev1.ProtocolTCP,
				}},
			},
		},
	}
	manager := NewManager(api)
	ctx := context.Background()
	if err := manager.Start(ctx, "minikube", "10.244.0.8", listener.Addr().String()); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = manager.StopAll(context.Background()) }()

	info, err := manager.StartIntercept(ctx, Mapping{
		Namespace: "default",
		Service:   "api",
		Ports: []PortMapping{{
			ServicePort: 80, Protocol: "TCP",
			LocalHost: "127.0.0.1", LocalPort: local.Addr().(*net.TCPAddr).Port,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}

	_ = manager.control.close()
	select {
	case <-manager.ControlLost():
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for control lost")
	}

	if err := manager.RecoverControl(ctx); err != nil {
		t.Fatalf("RecoverControl: %v", err)
	}
	select {
	case <-manager.ControlLost():
		t.Fatal("new control channel should not be closed")
	default:
	}

	client, err := net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", info.Ports[0].ListenPort))
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if _, err := client.Write([]byte("ping")); err != nil {
		t.Fatal(err)
	}
	_ = client.SetReadDeadline(time.Now().Add(3 * time.Second))
	buf := make([]byte, 64)
	n, err := client.Read(buf)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(buf[:n]); got != "local:ping" {
		t.Fatalf("got %q", got)
	}
	if len(manager.List()) != 1 {
		t.Fatalf("list=%d after recover", len(manager.List()))
	}
}

func TestStartMirrorFailsWhenControlChannelDrops(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := gateway.NewServer(log.New(io.Discard, "", 0), time.Second)
	go func() { _ = server.Serve(listener) }()

	api := &fakeCluster{
		service: &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{Name: "static-web", Namespace: "default"},
			Spec: corev1.ServiceSpec{
				ClusterIP: "10.96.1.10",
				Selector:  map[string]string{"role": "myrole"},
				Ports: []corev1.ServicePort{{
					Name: "http", Port: 80, Protocol: corev1.ProtocolTCP,
				}},
			},
		},
	}
	manager := NewManager(api)
	ctx := context.Background()
	if err := manager.Start(ctx, "minikube", "10.244.0.8", listener.Addr().String()); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = manager.StopAll(context.Background()) }()

	// Closing the listener only stops Accept; drop the live control conn.
	_ = manager.control.close()
	select {
	case <-manager.ControlLost():
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for control lost")
	}
	_ = listener.Close()

	_, err = manager.StartMirror(ctx, Mapping{
		Namespace: "default",
		Service:   "static-web",
		Ports: []PortMapping{{
			ServicePort: 80, Protocol: "TCP",
			LocalHost: "127.0.0.1", LocalPort: 18080,
		}},
	})
	if err == nil {
		t.Fatal("expected mirror start to fail after control drop")
	}
	if !errors.Is(err, errControlClosed) && !strings.Contains(err.Error(), "gateway control channel closed") {
		t.Fatalf("got %v, want control closed", err)
	}
}
