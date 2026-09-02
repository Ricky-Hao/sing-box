package group

import (
	"context"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/adapter/outbound"
	"github.com/sagernet/sing-box/common/interrupt"
	"github.com/sagernet/sing-box/common/urltest"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing/common"
	"github.com/sagernet/sing/common/batch"
	E "github.com/sagernet/sing/common/exceptions"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
	"github.com/sagernet/sing/common/x/list"
	"github.com/sagernet/sing/service"
	"github.com/sagernet/sing/service/pause"
)

func RegisterFallback(registry *outbound.Registry) {
	outbound.Register[option.FallbackOutboundOptions](registry, C.TypeFallback, NewFallback)
}

var (
	_ adapter.OutboundGroup = (*Fallback)(nil)
	_ adapter.URLTestGroup  = (*Fallback)(nil)
)

const fallbackURLTestSuccessThreshold = 2

type Fallback struct {
	outbound.Adapter
	ctx                          context.Context
	outbound                     adapter.OutboundManager
	connection                   adapter.ConnectionManager
	logger                       log.ContextLogger
	tags                         []string
	link                         string
	interval                     time.Duration
	idleTimeout                  time.Duration
	group                        *FallbackGroup
	interruptExternalConnections bool
}

func NewFallback(ctx context.Context, router adapter.Router, logger log.ContextLogger, tag string, options option.FallbackOutboundOptions) (adapter.Outbound, error) {
	fallback := &Fallback{
		Adapter:                      outbound.NewAdapter(C.TypeFallback, tag, []string{N.NetworkTCP, N.NetworkUDP}, options.Outbounds),
		ctx:                          ctx,
		outbound:                     service.FromContext[adapter.OutboundManager](ctx),
		connection:                   service.FromContext[adapter.ConnectionManager](ctx),
		logger:                       logger,
		tags:                         options.Outbounds,
		link:                         options.URL,
		interval:                     time.Duration(options.Interval),
		idleTimeout:                  time.Duration(options.IdleTimeout),
		interruptExternalConnections: options.InterruptExistConnections,
	}
	if len(fallback.tags) == 0 {
		return nil, E.New("missing tags")
	}
	return fallback, nil
}

func (s *Fallback) Start() error {
	outbounds := make([]adapter.Outbound, 0, len(s.tags))
	for i, tag := range s.tags {
		detour, loaded := s.outbound.Outbound(tag)
		if !loaded {
			return E.New("outbound ", i, " not found: ", tag)
		}
		outbounds = append(outbounds, detour)
	}
	group, err := NewFallbackGroup(s.ctx, s.outbound, s.logger, outbounds, s.link, s.interval, s.idleTimeout, s.interruptExternalConnections)
	if err != nil {
		return err
	}
	s.group = group
	return nil
}

func (s *Fallback) PostStart() error {
	s.group.PostStart()
	return nil
}

func (s *Fallback) Close() error {
	return common.Close(
		common.PtrOrNil(s.group),
	)
}

func (s *Fallback) Now() string {
	selectedOutboundTCP, selectedOutboundUDP := s.group.selectedOutbounds()
	if selectedOutboundTCP != nil {
		return selectedOutboundTCP.Tag()
	} else if selectedOutboundUDP != nil {
		return selectedOutboundUDP.Tag()
	}
	return ""
}

func (s *Fallback) All() []string {
	return s.tags
}

func (s *Fallback) URLTest(ctx context.Context) (map[string]uint16, error) {
	return s.group.URLTest(ctx)
}

func (s *Fallback) CheckOutbounds() {
	s.group.CheckOutbounds(true)
}

func (s *Fallback) PerformUpdateCheck() {
	s.group.performUpdateCheck()
}

func (s *Fallback) DialContext(ctx context.Context, network string, destination M.Socksaddr) (net.Conn, error) {
	s.group.Touch()
	var outbound adapter.Outbound
	switch N.NetworkName(network) {
	case N.NetworkTCP:
		outbound = s.group.selectedOutbound(N.NetworkTCP)
	case N.NetworkUDP:
		outbound = s.group.selectedOutbound(N.NetworkUDP)
	default:
		return nil, E.Extend(N.ErrUnknownNetwork, network)
	}
	if outbound == nil {
		outbound, _ = s.group.Select(network)
	}
	if outbound == nil {
		return nil, E.New("missing supported outbound")
	}
	conn, err := outbound.DialContext(ctx, network, destination)
	if err == nil {
		return s.group.interruptGroup.NewConn(conn, interrupt.IsExternalConnectionFromContext(ctx)), nil
	}
	s.logger.ErrorContext(ctx, err)
	s.group.deleteURLTestHistory(RealTag(s.outbound, outbound))
	s.group.performUpdateCheck()
	return nil, err
}

func (s *Fallback) ListenPacket(ctx context.Context, destination M.Socksaddr) (net.PacketConn, error) {
	s.group.Touch()
	outbound := s.group.selectedOutbound(N.NetworkUDP)
	if outbound == nil {
		outbound, _ = s.group.Select(N.NetworkUDP)
	}
	if outbound == nil {
		return nil, E.New("missing supported outbound")
	}
	conn, err := outbound.ListenPacket(ctx, destination)
	if err == nil {
		return s.group.interruptGroup.NewPacketConn(conn, interrupt.IsExternalConnectionFromContext(ctx)), nil
	}
	s.logger.ErrorContext(ctx, err)
	s.group.deleteURLTestHistory(RealTag(s.outbound, outbound))
	s.group.performUpdateCheck()
	return nil, err
}

func (s *Fallback) NewConnectionEx(ctx context.Context, conn net.Conn, metadata adapter.InboundContext, onClose N.CloseHandlerFunc) {
	ctx = interrupt.ContextWithIsExternalConnection(ctx)
	s.connection.NewConnection(ctx, s, conn, metadata, onClose)
}

func (s *Fallback) NewPacketConnectionEx(ctx context.Context, conn N.PacketConn, metadata adapter.InboundContext, onClose N.CloseHandlerFunc) {
	ctx = interrupt.ContextWithIsExternalConnection(ctx)
	s.connection.NewPacketConnection(ctx, s, conn, metadata, onClose)
}

type FallbackGroup struct {
	ctx                          context.Context
	outbound                     adapter.OutboundManager
	pause                        pause.Manager
	pauseCallback                *list.Element[pause.Callback]
	logger                       log.Logger
	outbounds                    []adapter.Outbound
	link                         string
	interval                     time.Duration
	probeInterval                time.Duration
	probeTimeout                 time.Duration
	idleTimeout                  time.Duration
	history                      *urltest.HistoryStorage
	urlTestSuccesses             map[string]uint8
	urlTestSuccessesAccess       sync.Mutex
	checking                     atomic.Bool
	selectedOutboundTCP          adapter.Outbound
	selectedOutboundUDP          adapter.Outbound
	selectedOutboundAccess       sync.RWMutex
	interruptGroup               *interrupt.Group
	interruptExternalConnections bool
	access                       sync.Mutex
	updateAccess                 sync.Mutex
	ticker                       *time.Ticker
	close                        context.CancelFunc
	started                      bool
	lastActive                   common.TypedValue[time.Time]
}

func NewFallbackGroup(ctx context.Context, outboundManager adapter.OutboundManager, logger log.Logger, outbounds []adapter.Outbound, link string, interval time.Duration, idleTimeout time.Duration, interruptExternalConnections bool) (*FallbackGroup, error) {
	if interval == 0 {
		interval = C.DefaultURLTestInterval
	}
	probeInterval, probeTimeout := fallbackProbeTiming(interval)
	if idleTimeout == 0 {
		idleTimeout = C.DefaultURLTestIdleTimeout
	}
	if interval > idleTimeout {
		return nil, E.New("interval must be less or equal than idle_timeout")
	}
	history := service.PtrFromContext[urltest.HistoryStorage](ctx)
	if history == nil {
		return nil, E.New("missing URL test history storage")
	}
	groupCtx, cancel := context.WithCancel(ctx)
	return &FallbackGroup{
		ctx:                          groupCtx,
		outbound:                     outboundManager,
		logger:                       logger,
		outbounds:                    outbounds,
		link:                         link,
		interval:                     interval,
		probeInterval:                probeInterval,
		probeTimeout:                 probeTimeout,
		idleTimeout:                  idleTimeout,
		history:                      history,
		urlTestSuccesses:             make(map[string]uint8),
		close:                        cancel,
		pause:                        service.FromContext[pause.Manager](ctx),
		interruptGroup:               interrupt.NewGroup(),
		interruptExternalConnections: interruptExternalConnections,
	}, nil
}

func (g *FallbackGroup) PostStart() {
	g.access.Lock()
	defer g.access.Unlock()
	g.started = true
	g.lastActive.Store(time.Now())
	go g.CheckOutbounds(false)
}

func (g *FallbackGroup) Touch() {
	if !g.started {
		return
	}
	g.access.Lock()
	defer g.access.Unlock()
	if g.ticker != nil {
		g.lastActive.Store(time.Now())
		return
	}
	ticker := time.NewTicker(g.probeInterval)
	g.ticker = ticker
	g.pauseCallback = pause.RegisterTicker(g.pause, ticker, g.probeInterval, nil)
	go g.loopCheck(ticker)
}

func (g *FallbackGroup) Close() error {
	g.access.Lock()
	defer g.access.Unlock()
	g.close()
	if g.ticker == nil {
		return nil
	}
	g.ticker.Stop()
	g.ticker = nil
	g.pause.UnregisterCallback(g.pauseCallback)
	g.pauseCallback = nil
	return nil
}

func (g *FallbackGroup) Select(network string) (adapter.Outbound, bool) {
	return g.selectOutbound(network, g.selectedOutbound(network))
}

func (g *FallbackGroup) selectedOutbound(network string) adapter.Outbound {
	g.selectedOutboundAccess.RLock()
	defer g.selectedOutboundAccess.RUnlock()
	switch network {
	case N.NetworkTCP:
		return g.selectedOutboundTCP
	case N.NetworkUDP:
		return g.selectedOutboundUDP
	}
	return nil
}

func (g *FallbackGroup) selectedOutbounds() (adapter.Outbound, adapter.Outbound) {
	g.selectedOutboundAccess.RLock()
	defer g.selectedOutboundAccess.RUnlock()
	return g.selectedOutboundTCP, g.selectedOutboundUDP
}

func (g *FallbackGroup) selectOutbound(network string, current adapter.Outbound) (adapter.Outbound, bool) {
	for _, detour := range g.outbounds {
		if !common.Contains(detour.Network(), network) {
			continue
		}
		if g.history.LoadURLTestHistory(RealTag(g.outbound, detour)) != nil && g.canSelect(detour, current) {
			return detour, true
		}
	}
	for _, detour := range g.outbounds {
		if !common.Contains(detour.Network(), network) {
			continue
		}
		return detour, false
	}
	return nil, false
}

func fallbackProbeTiming(interval time.Duration) (probeInterval time.Duration, probeTimeout time.Duration) {
	probeTimeout = min(C.TCPTimeout, interval/2)
	if probeTimeout <= 0 {
		probeTimeout = interval
	}
	probeInterval = interval - probeTimeout
	if probeInterval <= 0 {
		probeInterval = interval
	}
	return
}

func (g *FallbackGroup) loopCheck(ticker *time.Ticker) {
	if time.Since(g.lastActive.Load()) > g.probeInterval {
		g.lastActive.Store(time.Now())
		g.CheckOutbounds(false)
	}
	for {
		select {
		case <-g.ctx.Done():
			return
		case <-ticker.C:
		}
		if time.Since(g.lastActive.Load()) > g.idleTimeout {
			g.access.Lock()
			if g.ticker == ticker {
				g.ticker.Stop()
				g.ticker = nil
				g.pause.UnregisterCallback(g.pauseCallback)
				g.pauseCallback = nil
			}
			g.access.Unlock()
			return
		}
		g.CheckOutbounds(false)
	}
}

func (g *FallbackGroup) CheckOutbounds(force bool) {
	_, _ = g.urlTest(g.ctx, force, false)
}

func (g *FallbackGroup) URLTest(ctx context.Context) (map[string]uint16, error) {
	return g.urlTest(ctx, false, true)
}

func (g *FallbackGroup) urlTest(ctx context.Context, force bool, skipRecentHistory bool) (map[string]uint16, error) {
	result := make(map[string]uint16)
	if g.checking.Swap(true) {
		return result, nil
	}
	defer g.checking.Store(false)
	b, _ := batch.New(ctx, batch.WithConcurrencyNum[any](10))
	checked := make(map[string]bool)
	var resultAccess sync.Mutex
	for _, detour := range g.outbounds {
		tag := detour.Tag()
		realTag := RealTag(g.outbound, detour)
		if checked[realTag] {
			continue
		}
		history := g.history.LoadURLTestHistory(realTag)
		if skipRecentHistory && !force && history != nil && time.Since(history.Time) < g.interval {
			continue
		}
		checked[realTag] = true
		p, loaded := g.outbound.Outbound(realTag)
		if !loaded {
			continue
		}
		b.Go(realTag, func() (any, error) {
			testCtx, cancel := context.WithTimeout(ctx, g.probeTimeout)
			defer cancel()
			t, err := urltest.URLTest(testCtx, g.link, p)
			if err != nil {
				g.logger.Debug("outbound ", tag, " unavailable: ", err)
				g.deleteURLTestHistory(realTag)
			} else {
				g.logger.Debug("outbound ", tag, " available: ", t, "ms")
				g.reportURLTestSuccess(realTag, force || skipRecentHistory)
				g.history.StoreURLTestHistory(realTag, &adapter.URLTestHistory{
					Time:  time.Now(),
					Delay: t,
				})
				resultAccess.Lock()
				result[tag] = t
				resultAccess.Unlock()
			}
			return nil, nil
		})
	}
	b.Wait()
	g.performUpdateCheck()
	return result, nil
}

func (g *FallbackGroup) deleteURLTestHistory(realTag string) {
	g.clearURLTestSuccesses(realTag)
	g.history.DeleteURLTestHistory(realTag)
}

func (g *FallbackGroup) reportURLTestSuccess(realTag string, immediate bool) {
	g.urlTestSuccessesAccess.Lock()
	defer g.urlTestSuccessesAccess.Unlock()
	if immediate {
		g.urlTestSuccesses[realTag] = fallbackURLTestSuccessThreshold
		return
	}
	successes := g.urlTestSuccesses[realTag]
	if successes < fallbackURLTestSuccessThreshold {
		successes++
	}
	g.urlTestSuccesses[realTag] = successes
}

func (g *FallbackGroup) clearURLTestSuccesses(realTag string) {
	g.urlTestSuccessesAccess.Lock()
	delete(g.urlTestSuccesses, realTag)
	g.urlTestSuccessesAccess.Unlock()
}

func (g *FallbackGroup) canSelect(candidate adapter.Outbound, current adapter.Outbound) bool {
	if current == nil || candidate.Tag() == current.Tag() {
		return true
	}
	if g.history.LoadURLTestHistory(RealTag(g.outbound, current)) == nil {
		return true
	}
	candidateIndex := g.priorityIndex(candidate)
	currentIndex := g.priorityIndex(current)
	if candidateIndex == -1 || currentIndex == -1 || candidateIndex >= currentIndex {
		return true
	}
	g.urlTestSuccessesAccess.Lock()
	successes := g.urlTestSuccesses[RealTag(g.outbound, candidate)]
	g.urlTestSuccessesAccess.Unlock()
	return successes >= fallbackURLTestSuccessThreshold
}

func (g *FallbackGroup) priorityIndex(outbound adapter.Outbound) int {
	for i, detour := range g.outbounds {
		if detour.Tag() == outbound.Tag() {
			return i
		}
	}
	return -1
}

func (g *FallbackGroup) performUpdateCheck() {
	g.updateAccess.Lock()
	defer g.updateAccess.Unlock()
	selectedOutboundTCP, selectedOutboundUDP := g.selectedOutbounds()
	nextOutboundTCP := selectedOutboundTCP
	nextOutboundUDP := selectedOutboundUDP
	var updated bool
	if outbound, exists := g.selectOutbound(N.NetworkTCP, selectedOutboundTCP); outbound != nil && (selectedOutboundTCP == nil || (exists && outbound != selectedOutboundTCP)) {
		if selectedOutboundTCP != nil {
			updated = true
		}
		nextOutboundTCP = outbound
	}
	if outbound, exists := g.selectOutbound(N.NetworkUDP, selectedOutboundUDP); outbound != nil && (selectedOutboundUDP == nil || (exists && outbound != selectedOutboundUDP)) {
		if selectedOutboundUDP != nil {
			updated = true
		}
		nextOutboundUDP = outbound
	}
	g.selectedOutboundAccess.Lock()
	g.selectedOutboundTCP = nextOutboundTCP
	g.selectedOutboundUDP = nextOutboundUDP
	g.selectedOutboundAccess.Unlock()
	if updated {
		g.interruptGroup.Interrupt(g.interruptExternalConnections)
	}
}
