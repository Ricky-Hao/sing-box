package group

import (
	"context"
	"fmt"
	"net"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	outboundAdapter "github.com/sagernet/sing-box/adapter/outbound"
	"github.com/sagernet/sing-box/log"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
)

type fallbackRaceTestOutbound struct {
	outboundAdapter.Adapter
	dialCount   atomic.Int64
	listenCount atomic.Int64
	dialStarted chan<- struct{}
	dialRelease <-chan struct{}
}

func newFallbackRaceTestOutbound(tag string) *fallbackRaceTestOutbound {
	return &fallbackRaceTestOutbound{
		Adapter: outboundAdapter.NewAdapter("test", tag, []string{N.NetworkTCP, N.NetworkUDP}, nil),
	}
}

func (o *fallbackRaceTestOutbound) DialContext(context.Context, string, M.Socksaddr) (net.Conn, error) {
	o.dialCount.Add(1)
	if o.dialStarted != nil {
		o.dialStarted <- struct{}{}
		<-o.dialRelease
	}
	client, server := net.Pipe()
	_ = server.Close()
	return client, nil
}

func (o *fallbackRaceTestOutbound) ListenPacket(context.Context, M.Socksaddr) (net.PacketConn, error) {
	o.listenCount.Add(1)
	return net.ListenPacket("udp", "127.0.0.1:0")
}

func TestFallbackSelectionUsedByAllRequestSurfaces(t *testing.T) {
	primary := newFallbackRaceTestOutbound("primary")
	secondary := newFallbackRaceTestOutbound("secondary")
	group := newFallbackTestGroup(t, primary, secondary)
	markFallbackTestHealthy(group, primary)
	markFallbackTestHealthy(group, secondary)
	group.performUpdateCheck()
	fallback := &Fallback{
		logger: log.NewNOPFactory().Logger(),
		group:  group,
	}
	destination := M.ParseSocksaddrHostPortStr("example.com", "80")

	if selected := fallback.Now(); selected != primary.Tag() {
		t.Fatalf("expected Now to report primary, got %s", selected)
	}
	connection, err := fallback.DialContext(context.Background(), N.NetworkTCP, destination)
	if err != nil {
		t.Fatal(err)
	}
	_ = connection.Close()
	packetConnection, err := fallback.ListenPacket(context.Background(), destination)
	if err != nil {
		t.Fatal(err)
	}
	_ = packetConnection.Close()
	if primary.dialCount.Load() != 1 {
		t.Fatalf("expected primary to handle TCP request, got %d calls", primary.dialCount.Load())
	}
	if primary.listenCount.Load() != 1 {
		t.Fatalf("expected primary to handle UDP request, got %d calls", primary.listenCount.Load())
	}
	if secondary.dialCount.Load() != 0 || secondary.listenCount.Load() != 0 {
		t.Fatal("expected secondary to remain unused")
	}
}

func TestFallbackSelectionConcurrentUpdateAndRequests(t *testing.T) {
	primary := newFallbackRaceTestOutbound("primary")
	secondary := newFallbackRaceTestOutbound("secondary")
	group := newFallbackTestGroup(t, primary, secondary)
	markFallbackTestHealthy(group, primary)
	markFallbackTestHealthy(group, secondary)
	group.performUpdateCheck()
	fallback := &Fallback{
		logger: log.NewNOPFactory().Logger(),
		group:  group,
	}
	destination := M.ParseSocksaddrHostPortStr("example.com", "80")
	primaryTag := RealTag(group.outbound, primary)

	start := make(chan struct{})
	errCh := make(chan error, 3)
	var workers sync.WaitGroup
	workers.Add(4)
	go func() {
		defer workers.Done()
		<-start
		for range 2_000 {
			group.deleteURLTestHistory(primaryTag)
			group.performUpdateCheck()
			group.reportURLTestSuccess(primaryTag, true)
			markFallbackTestHealthy(group, primary)
			group.performUpdateCheck()
			runtime.Gosched()
		}
	}()
	go func() {
		defer workers.Done()
		<-start
		for range 4_000 {
			selected := fallback.Now()
			if selected != primary.Tag() && selected != secondary.Tag() {
				errCh <- fmt.Errorf("unexpected selected outbound %q", selected)
				return
			}
			runtime.Gosched()
		}
	}()
	go func() {
		defer workers.Done()
		<-start
		for range 200 {
			connection, err := fallback.DialContext(context.Background(), N.NetworkTCP, destination)
			if err != nil {
				errCh <- fmt.Errorf("dial: %w", err)
				return
			}
			_ = connection.Close()
		}
	}()
	go func() {
		defer workers.Done()
		<-start
		for range 200 {
			packetConnection, err := fallback.ListenPacket(context.Background(), destination)
			if err != nil {
				errCh <- fmt.Errorf("listen packet: %w", err)
				return
			}
			_ = packetConnection.Close()
		}
	}()
	close(start)
	workers.Wait()
	close(errCh)
	for err := range errCh {
		t.Fatal(err)
	}
	if primary.dialCount.Load()+secondary.dialCount.Load() != 200 {
		t.Fatal("expected every TCP request to use a selected outbound")
	}
	if primary.listenCount.Load()+secondary.listenCount.Load() != 200 {
		t.Fatal("expected every UDP request to use a selected outbound")
	}
}

func TestFallbackSelectionUpdateDoesNotWaitForDial(t *testing.T) {
	dialStarted := make(chan struct{})
	dialRelease := make(chan struct{})
	releaseDial := sync.OnceFunc(func() { close(dialRelease) })
	defer releaseDial()
	primary := newFallbackRaceTestOutbound("primary")
	primary.dialStarted = dialStarted
	primary.dialRelease = dialRelease
	secondary := newFallbackRaceTestOutbound("secondary")
	group := newFallbackTestGroup(t, primary, secondary)
	markFallbackTestHealthy(group, primary)
	markFallbackTestHealthy(group, secondary)
	group.performUpdateCheck()
	fallback := &Fallback{
		logger: log.NewNOPFactory().Logger(),
		group:  group,
	}
	dialCompleted := make(chan error)
	go func() {
		connection, err := fallback.DialContext(context.Background(), N.NetworkTCP, M.ParseSocksaddrHostPortStr("example.com", "80"))
		if err == nil {
			err = connection.Close()
		}
		dialCompleted <- err
	}()
	<-dialStarted

	group.deleteURLTestHistory(RealTag(group.outbound, primary))
	updateCompleted := make(chan struct{})
	go func() {
		group.performUpdateCheck()
		close(updateCompleted)
	}()
	select {
	case <-updateCompleted:
	case <-dialCompleted:
		t.Fatal("dial completed before it was released")
	case <-time.After(time.Second):
		t.Fatal("selection update waited for blocked network I/O")
	}
	releaseDial()
	select {
	case err := <-dialCompleted:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("dial did not complete after it was released")
	}
	if selected := fallback.Now(); selected != secondary.Tag() {
		t.Fatalf("expected update to select secondary, got %s", selected)
	}
}

var _ adapter.Outbound = (*fallbackRaceTestOutbound)(nil)
