package group

import (
	"context"
	"fmt"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/common/urltest"
	"github.com/sagernet/sing-box/log"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
	"github.com/sagernet/sing/service"
)

func newURLTestRaceGroup(t *testing.T, outbounds ...adapter.Outbound) *URLTestGroup {
	t.Helper()
	ctx := service.ContextWithPtr(context.Background(), urltest.NewHistoryStorage())
	manager := newFallbackTestOutboundManager(outbounds...)
	group, err := NewURLTestGroup(ctx, manager, log.NewNOPFactory().Logger(), outbounds, "", time.Minute, 50, 2*time.Minute, false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = group.Close() })
	return group
}

func markURLTestRaceHealthy(group *URLTestGroup, outbound adapter.Outbound, delay uint16) {
	group.history.StoreURLTestHistory(RealTag(group.outbound, outbound), &adapter.URLTestHistory{
		Time:  time.Now(),
		Delay: delay,
	})
}

func TestURLTestSelectionUsedByAllRequestSurfaces(t *testing.T) {
	primary := newFallbackRaceTestOutbound("primary")
	secondary := newFallbackRaceTestOutbound("secondary")
	group := newURLTestRaceGroup(t, primary, secondary)
	markURLTestRaceHealthy(group, primary, 1)
	markURLTestRaceHealthy(group, secondary, 100)
	group.performUpdateCheck()
	urlTest := &URLTest{
		logger: log.NewNOPFactory().Logger(),
		group:  group,
	}
	destination := M.ParseSocksaddrHostPortStr("example.com", "80")

	if selected := urlTest.Now(); selected != primary.Tag() {
		t.Fatalf("expected Now to report primary, got %s", selected)
	}
	connection, err := urlTest.DialContext(context.Background(), N.NetworkTCP, destination)
	if err != nil {
		t.Fatal(err)
	}
	_ = connection.Close()
	packetConnection, err := urlTest.ListenPacket(context.Background(), destination)
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

func TestURLTestSelectionConcurrentUpdateAndRequests(t *testing.T) {
	primary := newFallbackRaceTestOutbound("primary")
	secondary := newFallbackRaceTestOutbound("secondary")
	group := newURLTestRaceGroup(t, primary, secondary)
	markURLTestRaceHealthy(group, primary, 1)
	markURLTestRaceHealthy(group, secondary, 100)
	group.performUpdateCheck()
	urlTest := &URLTest{
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
			group.history.DeleteURLTestHistory(primaryTag)
			group.performUpdateCheck()
			markURLTestRaceHealthy(group, primary, 1)
			group.performUpdateCheck()
			runtime.Gosched()
		}
	}()
	go func() {
		defer workers.Done()
		<-start
		for range 4_000 {
			selected := urlTest.Now()
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
			connection, err := urlTest.DialContext(context.Background(), N.NetworkTCP, destination)
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
			packetConnection, err := urlTest.ListenPacket(context.Background(), destination)
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

func TestURLTestSelectionUpdateDoesNotWaitForDial(t *testing.T) {
	dialStarted := make(chan struct{})
	dialRelease := make(chan struct{})
	releaseDial := sync.OnceFunc(func() { close(dialRelease) })
	defer releaseDial()
	primary := newFallbackRaceTestOutbound("primary")
	primary.dialStarted = dialStarted
	primary.dialRelease = dialRelease
	secondary := newFallbackRaceTestOutbound("secondary")
	group := newURLTestRaceGroup(t, primary, secondary)
	markURLTestRaceHealthy(group, primary, 1)
	markURLTestRaceHealthy(group, secondary, 100)
	group.performUpdateCheck()
	urlTest := &URLTest{
		logger: log.NewNOPFactory().Logger(),
		group:  group,
	}
	dialCompleted := make(chan error)
	go func() {
		connection, err := urlTest.DialContext(context.Background(), N.NetworkTCP, M.ParseSocksaddrHostPortStr("example.com", "80"))
		if err == nil {
			err = connection.Close()
		}
		dialCompleted <- err
	}()
	<-dialStarted

	group.history.DeleteURLTestHistory(RealTag(group.outbound, primary))
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
	if selected := urlTest.Now(); selected != secondary.Tag() {
		t.Fatalf("expected update to select secondary, got %s", selected)
	}
}
