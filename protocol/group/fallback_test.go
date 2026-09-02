package group

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	outboundAdapter "github.com/sagernet/sing-box/adapter/outbound"
	"github.com/sagernet/sing-box/common/urltest"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/log"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
	"github.com/sagernet/sing/service"
)

type fallbackTestOutbound struct {
	outboundAdapter.Adapter
	dial        func(context.Context, string, M.Socksaddr) (net.Conn, error)
	dialErr     error
	listenErr   error
	dialCount   int
	listenCount int
}

type fallbackTestOutboundManager struct {
	outbounds map[string]adapter.Outbound
}

func newFallbackTestOutboundManager(outbounds ...adapter.Outbound) *fallbackTestOutboundManager {
	manager := &fallbackTestOutboundManager{
		outbounds: make(map[string]adapter.Outbound, len(outbounds)),
	}
	for _, outbound := range outbounds {
		manager.outbounds[outbound.Tag()] = outbound
	}
	return manager
}

func (m *fallbackTestOutboundManager) Start(stage adapter.StartStage) error {
	return nil
}

func (m *fallbackTestOutboundManager) Close() error {
	return nil
}

func (m *fallbackTestOutboundManager) Outbounds() []adapter.Outbound {
	outbounds := make([]adapter.Outbound, 0, len(m.outbounds))
	for _, outbound := range m.outbounds {
		outbounds = append(outbounds, outbound)
	}
	return outbounds
}

func (m *fallbackTestOutboundManager) Outbound(tag string) (adapter.Outbound, bool) {
	outbound, loaded := m.outbounds[tag]
	return outbound, loaded
}

func (m *fallbackTestOutboundManager) Default() adapter.Outbound {
	return nil
}

func (m *fallbackTestOutboundManager) Remove(tag string) error {
	delete(m.outbounds, tag)
	return nil
}

func (m *fallbackTestOutboundManager) Create(ctx context.Context, router adapter.Router, logger log.ContextLogger, tag string, outboundType string, options any) error {
	return nil
}

func newFallbackTestOutbound(tag string, networks []string) *fallbackTestOutbound {
	return &fallbackTestOutbound{
		Adapter: outboundAdapter.NewAdapter("test", tag, networks, nil),
	}
}

func (o *fallbackTestOutbound) DialContext(ctx context.Context, network string, destination M.Socksaddr) (net.Conn, error) {
	o.dialCount++
	if o.dial != nil {
		return o.dial(ctx, network, destination)
	}
	if o.dialErr != nil {
		return nil, o.dialErr
	}
	client, server := net.Pipe()
	_ = server.Close()
	return client, nil
}

func (o *fallbackTestOutbound) ListenPacket(ctx context.Context, destination M.Socksaddr) (net.PacketConn, error) {
	o.listenCount++
	if o.listenErr != nil {
		return nil, o.listenErr
	}
	return net.ListenPacket("udp", "127.0.0.1:0")
}

func newFallbackTestGroup(t *testing.T, outbounds ...adapter.Outbound) *FallbackGroup {
	t.Helper()
	ctx := service.ContextWithPtr(context.Background(), urltest.NewHistoryStorage())
	manager := newFallbackTestOutboundManager(outbounds...)
	group, err := NewFallbackGroup(ctx, manager, log.NewNOPFactory().Logger(), outbounds, "", time.Minute, 2*time.Minute, false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = group.Close() })
	return group
}

func markFallbackTestHealthy(group *FallbackGroup, outbound adapter.Outbound) {
	group.history.StoreURLTestHistory(RealTag(group.outbound, outbound), &adapter.URLTestHistory{
		Time:  time.Now(),
		Delay: 100,
	})
}

func TestFallbackSelectsFirstHealthyOutbound(t *testing.T) {
	t.Parallel()

	primary := newFallbackTestOutbound("primary", []string{N.NetworkTCP, N.NetworkUDP})
	secondary := newFallbackTestOutbound("secondary", []string{N.NetworkTCP, N.NetworkUDP})
	tertiary := newFallbackTestOutbound("tertiary", []string{N.NetworkTCP, N.NetworkUDP})
	group := newFallbackTestGroup(t, primary, secondary, tertiary)

	markFallbackTestHealthy(group, secondary)
	markFallbackTestHealthy(group, tertiary)

	selected, exists := group.Select(N.NetworkTCP)
	if !exists {
		t.Fatal("expected a healthy outbound")
	}
	if selected != secondary {
		t.Fatalf("expected secondary, got %s", selected.Tag())
	}

	markFallbackTestHealthy(group, primary)
	selected, exists = group.Select(N.NetworkTCP)
	if !exists {
		t.Fatal("expected a healthy outbound after primary recovers")
	}
	if selected != primary {
		t.Fatalf("expected primary after recovery, got %s", selected.Tag())
	}
}

func TestFallbackCheckOutboundsRefreshesRecentHistory(t *testing.T) {
	t.Parallel()

	primary := newFallbackTestOutbound("primary", []string{N.NetworkTCP, N.NetworkUDP})
	group := newFallbackTestGroup(t, primary)
	group.outbound = newFallbackTestOutboundManager(primary)
	markFallbackTestHealthy(group, primary)

	group.CheckOutbounds(false)

	if primary.dialCount == 0 {
		t.Fatal("expected periodic check to probe outbound even with recent history")
	}
	if history := group.history.LoadURLTestHistory(RealTag(group.outbound, primary)); history != nil {
		t.Fatal("expected failed periodic check to delete recent history")
	}
}

func TestFallbackCheckOutboundsSupportsTCPURL(t *testing.T) {
	t.Parallel()

	primary := newFallbackTestOutbound("primary", []string{N.NetworkTCP, N.NetworkUDP})
	group := newFallbackTestGroup(t, primary)
	group.outbound = newFallbackTestOutboundManager(primary)
	group.link = "tcp://example.com:443"

	group.CheckOutbounds(false)

	if primary.dialCount != 1 {
		t.Fatalf("expected tcp probe to dial once, got %d", primary.dialCount)
	}
	if history := group.history.LoadURLTestHistory(RealTag(group.outbound, primary)); history == nil {
		t.Fatal("expected tcp probe to store history")
	}
}

func TestFallbackForceCheckDeletesRecentHistory(t *testing.T) {
	t.Parallel()

	primary := newFallbackTestOutbound("primary", []string{N.NetworkTCP, N.NetworkUDP})
	group := newFallbackTestGroup(t, primary)
	group.outbound = newFallbackTestOutboundManager(primary)
	markFallbackTestHealthy(group, primary)

	group.CheckOutbounds(true)

	if history := group.history.LoadURLTestHistory(RealTag(group.outbound, primary)); history != nil {
		t.Fatal("expected force check failure to delete recent history")
	}
}

func TestFallbackPeriodicFailureSwitchesImmediately(t *testing.T) {
	t.Parallel()

	primary := newFallbackTestOutbound("primary", []string{N.NetworkTCP, N.NetworkUDP})
	secondary := newFallbackTestOutbound("secondary", []string{N.NetworkTCP, N.NetworkUDP})
	group := newFallbackTestGroup(t, primary, secondary)
	markFallbackTestHealthy(group, primary)
	markFallbackTestHealthy(group, secondary)
	group.performUpdateCheck()
	if group.selectedOutboundTCP != primary {
		t.Fatalf("expected primary selected, got %s", group.selectedOutboundTCP.Tag())
	}

	group.deleteURLTestHistory(RealTag(group.outbound, primary))
	group.performUpdateCheck()
	if group.selectedOutboundTCP != secondary {
		t.Fatalf("expected secondary after periodic failure, got %s", group.selectedOutboundTCP.Tag())
	}
}

func TestFallbackPeriodicSuccessDebouncesFailback(t *testing.T) {
	t.Parallel()

	primary := newFallbackTestOutbound("primary", []string{N.NetworkTCP, N.NetworkUDP})
	secondary := newFallbackTestOutbound("secondary", []string{N.NetworkTCP, N.NetworkUDP})
	group := newFallbackTestGroup(t, primary, secondary)
	markFallbackTestHealthy(group, primary)
	markFallbackTestHealthy(group, secondary)
	group.performUpdateCheck()
	if group.selectedOutboundTCP != primary {
		t.Fatalf("expected primary selected, got %s", group.selectedOutboundTCP.Tag())
	}

	group.deleteURLTestHistory(RealTag(group.outbound, primary))
	group.performUpdateCheck()
	if group.selectedOutboundTCP != secondary {
		t.Fatalf("expected secondary after primary failure, got %s", group.selectedOutboundTCP.Tag())
	}

	group.reportURLTestSuccess(RealTag(group.outbound, primary), false)
	markFallbackTestHealthy(group, primary)
	group.performUpdateCheck()
	if group.selectedOutboundTCP != secondary {
		t.Fatalf("expected secondary to stay selected after first primary recovery probe, got %s", group.selectedOutboundTCP.Tag())
	}

	group.reportURLTestSuccess(RealTag(group.outbound, primary), false)
	markFallbackTestHealthy(group, primary)
	group.performUpdateCheck()
	if group.selectedOutboundTCP != primary {
		t.Fatalf("expected primary after second primary recovery probe, got %s", group.selectedOutboundTCP.Tag())
	}
}

func TestFallbackForceSuccessSwitchesBackImmediately(t *testing.T) {
	t.Parallel()

	primary := newFallbackTestOutbound("primary", []string{N.NetworkTCP, N.NetworkUDP})
	secondary := newFallbackTestOutbound("secondary", []string{N.NetworkTCP, N.NetworkUDP})
	group := newFallbackTestGroup(t, primary, secondary)
	markFallbackTestHealthy(group, primary)
	markFallbackTestHealthy(group, secondary)
	group.performUpdateCheck()

	group.deleteURLTestHistory(RealTag(group.outbound, primary))
	group.performUpdateCheck()
	if group.selectedOutboundTCP != secondary {
		t.Fatalf("expected secondary after primary failure, got %s", group.selectedOutboundTCP.Tag())
	}

	group.reportURLTestSuccess(RealTag(group.outbound, primary), true)
	markFallbackTestHealthy(group, primary)
	group.performUpdateCheck()
	if group.selectedOutboundTCP != primary {
		t.Fatalf("expected primary after forced recovery probe, got %s", group.selectedOutboundTCP.Tag())
	}
}

func TestFallbackProbeTimingFitsInterval(t *testing.T) {
	t.Parallel()

	probeInterval, probeTimeout := fallbackProbeTiming(10 * time.Second)
	if probeInterval != 5*time.Second {
		t.Fatalf("expected probe interval 5s, got %s", probeInterval)
	}
	if probeTimeout != 5*time.Second {
		t.Fatalf("expected probe timeout 5s, got %s", probeTimeout)
	}
	if probeInterval+probeTimeout != 10*time.Second {
		t.Fatalf("expected probe timing to fit configured interval, got %s", probeInterval+probeTimeout)
	}
}

func TestFallbackProbeTimingCapsTimeout(t *testing.T) {
	t.Parallel()

	interval := time.Minute
	probeInterval, probeTimeout := fallbackProbeTiming(interval)
	if probeTimeout != C.TCPTimeout {
		t.Fatalf("expected probe timeout %s, got %s", C.TCPTimeout, probeTimeout)
	}
	if probeInterval+probeTimeout != interval {
		t.Fatalf("expected probe timing to fit configured interval, got %s", probeInterval+probeTimeout)
	}
}

func TestFallbackSelectsFirstSupportedOutboundWithoutHistory(t *testing.T) {
	t.Parallel()

	primary := newFallbackTestOutbound("primary", []string{N.NetworkTCP})
	secondary := newFallbackTestOutbound("secondary", []string{N.NetworkTCP})
	group := newFallbackTestGroup(t, primary, secondary)

	selected, exists := group.Select(N.NetworkTCP)
	if exists {
		t.Fatal("did not expect a healthy outbound")
	}
	if selected != primary {
		t.Fatalf("expected primary fallback, got %s", selected.Tag())
	}
}

func TestFallbackSelectFiltersByNetwork(t *testing.T) {
	t.Parallel()

	tcpOnly := newFallbackTestOutbound("tcp-only", []string{N.NetworkTCP})
	udpOnly := newFallbackTestOutbound("udp-only", []string{N.NetworkUDP})
	group := newFallbackTestGroup(t, tcpOnly, udpOnly)

	markFallbackTestHealthy(group, tcpOnly)
	markFallbackTestHealthy(group, udpOnly)

	selected, exists := group.Select(N.NetworkUDP)
	if !exists {
		t.Fatal("expected a healthy UDP outbound")
	}
	if selected != udpOnly {
		t.Fatalf("expected udp-only, got %s", selected.Tag())
	}
}

func TestFallbackUpdateSwitchesDownAndBackUpAfterStableRecovery(t *testing.T) {
	t.Parallel()

	primary := newFallbackTestOutbound("primary", []string{N.NetworkTCP, N.NetworkUDP})
	secondary := newFallbackTestOutbound("secondary", []string{N.NetworkTCP, N.NetworkUDP})
	group := newFallbackTestGroup(t, primary, secondary)

	markFallbackTestHealthy(group, primary)
	markFallbackTestHealthy(group, secondary)
	group.performUpdateCheck()
	if group.selectedOutboundTCP != primary {
		t.Fatalf("expected primary selected, got %s", group.selectedOutboundTCP.Tag())
	}

	group.deleteURLTestHistory(RealTag(group.outbound, primary))
	group.performUpdateCheck()
	if group.selectedOutboundTCP != secondary {
		t.Fatalf("expected secondary after primary failure, got %s", group.selectedOutboundTCP.Tag())
	}

	group.reportURLTestSuccess(RealTag(group.outbound, primary), false)
	markFallbackTestHealthy(group, primary)
	group.performUpdateCheck()
	if group.selectedOutboundTCP != secondary {
		t.Fatalf("expected secondary to stay selected after first primary recovery probe, got %s", group.selectedOutboundTCP.Tag())
	}

	group.reportURLTestSuccess(RealTag(group.outbound, primary), false)
	markFallbackTestHealthy(group, primary)
	group.performUpdateCheck()
	if group.selectedOutboundTCP != primary {
		t.Fatalf("expected primary after stable recovery, got %s", group.selectedOutboundTCP.Tag())
	}
}

func TestFallbackUpdateKeepsCurrentWhenAllUnhealthyThenSwitchesBack(t *testing.T) {
	t.Parallel()

	primary := newFallbackTestOutbound("primary", []string{N.NetworkTCP, N.NetworkUDP})
	secondary := newFallbackTestOutbound("secondary", []string{N.NetworkTCP, N.NetworkUDP})
	group := newFallbackTestGroup(t, primary, secondary)

	markFallbackTestHealthy(group, primary)
	markFallbackTestHealthy(group, secondary)
	group.performUpdateCheck()

	group.deleteURLTestHistory(RealTag(group.outbound, primary))
	group.performUpdateCheck()
	if group.selectedOutboundTCP != secondary {
		t.Fatalf("expected secondary after primary failure, got %s", group.selectedOutboundTCP.Tag())
	}

	group.deleteURLTestHistory(RealTag(group.outbound, secondary))
	group.performUpdateCheck()
	if group.selectedOutboundTCP != secondary {
		t.Fatalf("expected current selection to stay on secondary while all outbounds are unhealthy, got %s", group.selectedOutboundTCP.Tag())
	}

	markFallbackTestHealthy(group, primary)
	group.performUpdateCheck()
	if group.selectedOutboundTCP != primary {
		t.Fatalf("expected primary after recovery, got %s", group.selectedOutboundTCP.Tag())
	}
}

func TestFallbackUpdateInterruptsConnectionsOnSwitch(t *testing.T) {
	t.Parallel()

	primary := newFallbackTestOutbound("primary", []string{N.NetworkTCP, N.NetworkUDP})
	secondary := newFallbackTestOutbound("secondary", []string{N.NetworkTCP, N.NetworkUDP})
	group := newFallbackTestGroup(t, primary, secondary)

	markFallbackTestHealthy(group, primary)
	markFallbackTestHealthy(group, secondary)
	group.performUpdateCheck()

	client, server := net.Pipe()
	wrapped := group.interruptGroup.NewConn(client, false)
	defer wrapped.Close()
	defer server.Close()
	if err := server.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}

	group.deleteURLTestHistory(RealTag(group.outbound, primary))
	group.performUpdateCheck()

	if group.selectedOutboundTCP != secondary {
		t.Fatalf("expected secondary after primary failure, got %s", group.selectedOutboundTCP.Tag())
	}
	var buffer [1]byte
	if _, err := server.Read(buffer[:]); err == nil {
		t.Fatal("expected existing connection to be interrupted")
	}
}

func TestFallbackDialFailureDeletesHistory(t *testing.T) {
	t.Parallel()

	primary := newFallbackTestOutbound("primary", []string{N.NetworkTCP})
	primary.dialErr = errors.New("dial failed")
	group := newFallbackTestGroup(t, primary)
	markFallbackTestHealthy(group, primary)

	fallback := &Fallback{
		logger: log.NewNOPFactory().Logger(),
		group:  group,
	}
	_, err := fallback.DialContext(context.Background(), N.NetworkTCP, M.ParseSocksaddrHostPortStr("example.com", "80"))
	if err == nil {
		t.Fatal("expected dial error")
	}
	if history := group.history.LoadURLTestHistory(RealTag(group.outbound, primary)); history != nil {
		t.Fatal("expected history to be deleted after dial failure")
	}
}

func TestFallbackDialFailureSwitchesNextRequest(t *testing.T) {
	t.Parallel()

	primary := newFallbackTestOutbound("primary", []string{N.NetworkTCP})
	secondary := newFallbackTestOutbound("secondary", []string{N.NetworkTCP})
	group := newFallbackTestGroup(t, primary, secondary)
	markFallbackTestHealthy(group, primary)
	markFallbackTestHealthy(group, secondary)
	group.performUpdateCheck()
	if group.selectedOutboundTCP != primary {
		t.Fatalf("expected primary selected, got %s", group.selectedOutboundTCP.Tag())
	}

	primary.dialErr = errors.New("dial failed")
	fallback := &Fallback{
		logger: log.NewNOPFactory().Logger(),
		group:  group,
	}
	_, err := fallback.DialContext(context.Background(), N.NetworkTCP, M.ParseSocksaddrHostPortStr("example.com", "80"))
	if err == nil {
		t.Fatal("expected first request to fail without retry")
	}
	if group.selectedOutboundTCP != secondary {
		t.Fatalf("expected selected outbound to switch to secondary after failure, got %s", group.selectedOutboundTCP.Tag())
	}

	conn, err := fallback.DialContext(context.Background(), N.NetworkTCP, M.ParseSocksaddrHostPortStr("example.com", "80"))
	if err != nil {
		t.Fatal(err)
	}
	_ = conn.Close()
	if primary.dialCount != 1 {
		t.Fatalf("expected primary to be dialed once, got %d", primary.dialCount)
	}
	if secondary.dialCount != 1 {
		t.Fatalf("expected secondary to handle the next request, got %d dials", secondary.dialCount)
	}
}

func TestFallbackListenPacketFailureSwitchesNextRequest(t *testing.T) {
	t.Parallel()

	primary := newFallbackTestOutbound("primary", []string{N.NetworkUDP})
	secondary := newFallbackTestOutbound("secondary", []string{N.NetworkUDP})
	group := newFallbackTestGroup(t, primary, secondary)
	markFallbackTestHealthy(group, primary)
	markFallbackTestHealthy(group, secondary)
	group.performUpdateCheck()
	if group.selectedOutboundUDP != primary {
		t.Fatalf("expected primary selected, got %s", group.selectedOutboundUDP.Tag())
	}

	primary.listenErr = errors.New("listen failed")
	fallback := &Fallback{
		logger: log.NewNOPFactory().Logger(),
		group:  group,
	}
	_, err := fallback.ListenPacket(context.Background(), M.ParseSocksaddrHostPortStr("example.com", "80"))
	if err == nil {
		t.Fatal("expected first request to fail without retry")
	}
	if group.selectedOutboundUDP != secondary {
		t.Fatalf("expected selected outbound to switch to secondary after failure, got %s", group.selectedOutboundUDP.Tag())
	}

	conn, err := fallback.ListenPacket(context.Background(), M.ParseSocksaddrHostPortStr("example.com", "80"))
	if err != nil {
		t.Fatal(err)
	}
	_ = conn.Close()
	if primary.listenCount != 1 {
		t.Fatalf("expected primary to listen once, got %d", primary.listenCount)
	}
	if secondary.listenCount != 1 {
		t.Fatalf("expected secondary to handle the next request, got %d listens", secondary.listenCount)
	}
}

func TestFallbackDisplayName(t *testing.T) {
	t.Parallel()

	if displayName := C.ProxyDisplayName(C.TypeFallback); displayName != "Fallback" {
		t.Fatalf("expected Fallback display name, got %s", displayName)
	}
}

func TestFallbackURLTestReturnsWhenCallerCancels(t *testing.T) {
	t.Parallel()

	started := make(chan struct{})
	primary := newFallbackTestOutbound("primary", []string{N.NetworkTCP})
	primary.dial = func(ctx context.Context, network string, destination M.Socksaddr) (net.Conn, error) {
		close(started)
		<-ctx.Done()
		return nil, ctx.Err()
	}
	group := newFallbackTestGroup(t, primary)
	group.link = "tcp://example.com:443"
	ctx, cancel := context.WithCancel(context.Background())
	completed := make(chan struct{})
	go func() {
		_, _ = group.URLTest(ctx)
		close(completed)
	}()
	<-started

	cancel()

	select {
	case <-completed:
	case <-time.After(time.Second):
		t.Fatal("URL test did not stop after caller cancellation")
	}
}

func TestFallbackCloseCancelsActiveProbe(t *testing.T) {
	t.Parallel()

	started := make(chan struct{})
	primary := newFallbackTestOutbound("primary", []string{N.NetworkTCP})
	primary.dial = func(ctx context.Context, network string, destination M.Socksaddr) (net.Conn, error) {
		close(started)
		<-ctx.Done()
		return nil, ctx.Err()
	}
	group := newFallbackTestGroup(t, primary)
	group.link = "tcp://example.com:443"
	completed := make(chan struct{})
	go func() {
		group.CheckOutbounds(false)
		close(completed)
	}()
	<-started

	if err := group.Close(); err != nil {
		t.Fatal(err)
	}

	select {
	case <-completed:
	case <-time.After(time.Second):
		t.Fatal("active probe did not stop after group close")
	}
}

func TestURLTestOutboundsUsesNestedFallbackURLTestGroup(t *testing.T) {
	t.Parallel()

	leaf := newFallbackTestOutbound("leaf", []string{N.NetworkTCP})
	manager := newFallbackTestOutboundManager(leaf)
	history := urltest.NewHistoryStorage()
	ctx := service.ContextWithPtr(context.Background(), history)
	fallback := &Fallback{
		Adapter: outboundAdapter.NewAdapter(C.TypeFallback, "fallback", []string{N.NetworkTCP}, []string{"leaf"}),
	}
	manager.outbounds[fallback.Tag()] = fallback
	fallbackGroup, err := NewFallbackGroup(ctx, manager, log.NewNOPFactory().Logger(), []adapter.Outbound{leaf}, "tcp://example.com:443", time.Minute, 2*time.Minute, false)
	if err != nil {
		t.Fatal(err)
	}
	fallback.group = fallbackGroup
	t.Cleanup(func() { _ = fallback.Close() })

	result := URLTestOutbounds(ctx, manager, history, log.NewNOPFactory().Logger(), []adapter.Outbound{fallback}, "", time.Minute, true)

	if _, exists := result["leaf"]; !exists {
		t.Fatal("expected nested fallback member result")
	}
	if _, exists := result["fallback"]; !exists {
		t.Fatal("expected nested fallback group result")
	}
}
