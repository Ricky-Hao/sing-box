//go:build with_gvisor

package iwan

import (
	"context"
	"encoding/binary"
	"net"
	"net/netip"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-tun"
	E "github.com/sagernet/sing/common/exceptions"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
)

type Endpoint struct {
	options EndpointOptions
	ctx     context.Context
	cancel  context.CancelFunc

	device *stackDevice
	conn   net.Conn

	access       sync.Mutex
	writeAccess  sync.Mutex
	fragmentLock sync.Mutex
	started      atomic.Bool
	ready        atomic.Bool
	state        endpointState
	token        [2]byte
	sessionID    [4]byte
	xorKey       [8]byte
	encrypt      bool
	lastRecv     time.Time
	lastOpen     time.Time
	echoCounter  uint32
	curDelay     uint32
	minDelay     uint32
	maxDelay     uint32
	routeMagic   uint32
	localAddress netip.Prefix
	fragments    fragmentReassembler
}

func NewEndpoint(options EndpointOptions) (*Endpoint, error) {
	if options.Dialer == nil {
		return nil, E.New("missing dialer")
	}
	if !options.Server.IsValid() {
		return nil, E.New("missing server")
	}
	if options.Username == "" {
		return nil, E.New("missing username")
	}
	if options.Password == "" {
		return nil, E.New("missing password")
	}
	if options.Server.Port == 0 {
		options.Server.Port = defaultPort
	}
	if options.MTU == 0 {
		options.MTU = defaultMTU
	}
	if options.MTU < minMTU || options.MTU > maxMTU {
		return nil, E.New("invalid MTU: ", options.MTU, ", required ", minMTU, "-", maxMTU)
	}
	if options.PipeID > 32767 {
		return nil, E.New("invalid pipe_id: ", options.PipeID, ", required 0-32767")
	}
	if options.PipeIndex > 1 {
		return nil, E.New("invalid pipe_index: ", options.PipeIndex, ", required 0 or 1")
	}
	ctx, cancel := context.WithCancel(options.Context)
	options.Context = ctx
	device, err := newStackDevice(options)
	if err != nil {
		cancel()
		return nil, err
	}
	return &Endpoint{
		options:  options,
		ctx:      ctx,
		cancel:   cancel,
		device:   device,
		state:    stateNotReady,
		xorKey:   deriveXORKey(options.Username, options.Password),
		encrypt:  options.Encrypt,
		minDelay: ^uint32(0),
	}, nil
}

func (e *Endpoint) Start() error {
	if e.started.Swap(true) {
		return nil
	}
	conn, err := e.options.Dialer.DialContext(e.ctx, N.NetworkUDP, e.options.Server)
	if err != nil {
		e.started.Store(false)
		return err
	}
	e.access.Lock()
	e.conn = conn
	e.state = stateAuthSent
	e.lastRecv = time.Now()
	e.lastOpen = time.Time{}
	e.access.Unlock()
	go e.readLoop(conn)
	go e.deviceLoop()
	go e.timerLoop()
	return nil
}

func (e *Endpoint) Ready() bool {
	return e.ready.Load()
}

func (e *Endpoint) WaitReady(ctx context.Context) error {
	if e.Ready() {
		return nil
	}
	if !e.started.Load() {
		return net.ErrClosed
	}
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if e.Ready() {
				return nil
			}
			if !e.started.Load() {
				return net.ErrClosed
			}
		}
	}
}

func (e *Endpoint) LocalAddresses() []netip.Prefix {
	e.access.Lock()
	defer e.access.Unlock()
	if !e.localAddress.IsValid() {
		return nil
	}
	return []netip.Prefix{e.localAddress}
}

func (e *Endpoint) DialContext(ctx context.Context, network string, destination M.Socksaddr) (net.Conn, error) {
	if !destination.Addr.IsValid() {
		return nil, E.Cause(os.ErrInvalid, "invalid non-IP destination")
	}
	return e.device.DialContext(ctx, network, destination)
}

func (e *Endpoint) ListenPacket(ctx context.Context, destination M.Socksaddr) (net.PacketConn, error) {
	if !destination.Addr.IsValid() {
		return nil, E.Cause(os.ErrInvalid, "invalid non-IP destination")
	}
	return e.device.ListenPacket(ctx, destination)
}

func (e *Endpoint) NewDirectRouteConnection(metadata adapter.InboundContext, routeContext tun.DirectRouteContext, timeout time.Duration) (tun.DirectRouteDestination, error) {
	return e.device.CreateDestination(metadata, routeContext, timeout)
}

func (e *Endpoint) Close() error {
	e.ready.Store(false)
	e.started.Store(false)
	e.access.Lock()
	conn := e.conn
	state := e.state
	token := e.token
	sessionID := e.sessionID
	if e.state == stateEstablished && conn != nil {
		e.state = stateClosed
	}
	e.conn = nil
	if e.state != stateClosed {
		e.state = stateClosed
	}
	e.access.Unlock()
	if state == stateEstablished && conn != nil {
		_ = e.writePacketTo(conn, buildClosePacket(token, sessionID))
	}
	e.cancel()
	if conn != nil {
		_ = conn.Close()
	}
	return e.device.Close()
}

func (e *Endpoint) readLoop(conn net.Conn) {
	buffer := make([]byte, fragmentOutputSize)
	for {
		n, err := conn.Read(buffer)
		if err != nil {
			select {
			case <-e.ctx.Done():
				return
			default:
			}
			if e.isCurrentConn(conn) {
				e.options.Logger.Error(E.Cause(err, "read iWAN packet"))
				e.markClosed(conn)
			}
			return
		}
		packet := make([]byte, n)
		copy(packet, buffer[:n])
		if e.handlePacket(conn, packet) {
			e.access.Lock()
			if e.conn == conn {
				e.lastRecv = time.Now()
			}
			e.access.Unlock()
		}
	}
}

func (e *Endpoint) deviceLoop() {
	buffer := make([]byte, fragmentOutputSize)
	for {
		n, err := e.device.Read(buffer)
		if err != nil {
			select {
			case <-e.ctx.Done():
				return
			default:
			}
			e.options.Logger.Error(E.Cause(err, "read iWAN stack packet"))
			return
		}
		e.access.Lock()
		if e.conn == nil || e.state != stateEstablished || !e.ready.Load() {
			e.access.Unlock()
			continue
		}
		conn := e.conn
		encrypt := e.encrypt
		token := e.token
		sessionID := e.sessionID
		xorKey := e.xorKey
		e.access.Unlock()
		packet := make([]byte, headerSize+n)
		if encrypt {
			packet[0] = packetDataEnc
			packet[1] = 1
			copy(packet[headerSize:], buffer[:n])
			xorData(xorKey, packet[headerSize:])
		} else {
			packet[0] = packetData
			copy(packet[headerSize:], buffer[:n])
		}
		copy(packet[2:4], token[:])
		copy(packet[4:8], sessionID[:])
		if err = e.writePacketTo(conn, packet); err != nil && e.isCurrentConn(conn) {
			e.options.Logger.Error(E.Cause(err, "write iWAN data packet"))
		}
	}
}

func (e *Endpoint) timerLoop() {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-e.ctx.Done():
			return
		case now := <-ticker.C:
			e.onTimer(now)
		}
	}
}

func (e *Endpoint) onTimer(now time.Time) {
	e.access.Lock()
	state := e.state
	switch state {
	case stateAuthSent:
		if now.Sub(e.lastRecv) > authTimeout {
			e.ready.Store(false)
			e.state = stateClosed
			e.access.Unlock()
			if err := e.reconnect(now); err != nil {
				e.options.Logger.Error(E.Cause(err, "reconnect iWAN"))
			}
			return
		}
		if now.Sub(e.lastOpen) < authRetryInterval {
			e.access.Unlock()
			return
		}
		e.lastOpen = now
		e.access.Unlock()
		packet, err := buildOpenPacket(e.options.Username, e.options.Password, e.options.MTU, e.options.Encrypt, e.options.PipeID, e.options.PipeIndex)
		if err != nil {
			e.options.Logger.Error(E.Cause(err, "build iWAN OPEN"))
			return
		}
		if err = e.writePacket(packet); err != nil {
			e.options.Logger.Error(E.Cause(err, "send iWAN OPEN"))
		}
	case stateEstablished:
		if now.Sub(e.lastRecv) > dataTimeout {
			conn := e.conn
			token := e.token
			sessionID := e.sessionID
			e.ready.Store(false)
			e.state = stateClosed
			e.access.Unlock()
			if conn != nil {
				_ = e.writePacketTo(conn, buildClosePacket(token, sessionID))
			}
			if err := e.reconnect(now); err != nil {
				e.options.Logger.Error(E.Cause(err, "reconnect iWAN"))
			}
			return
		}
		e.echoCounter++
		if e.echoCounter&1 != 0 {
			e.access.Unlock()
			return
		}
		packet := buildEchoPacket(e.token, e.sessionID, e.curDelay, e.minDelay, e.maxDelay, e.routeMagic, now)
		e.access.Unlock()
		if err := e.writePacket(packet); err != nil {
			e.options.Logger.Error(E.Cause(err, "send iWAN ECHO"))
		}
	case stateClosed:
		e.ready.Store(false)
		e.access.Unlock()
		if err := e.reconnect(now); err != nil {
			e.options.Logger.Error(E.Cause(err, "reconnect iWAN"))
		}
	default:
		e.access.Unlock()
	}
}

func (e *Endpoint) handlePacket(conn net.Conn, packet []byte) bool {
	if len(packet) < headerSize {
		return false
	}
	if !e.isCurrentConn(conn) {
		return false
	}
	switch packet[0] {
	case packetData, packetDataEnc:
		return e.handleData(conn, packet)
	case packetOpenAck:
		return e.handleOpenAck(conn, packet)
	case packetOpenReject:
		return e.handleOpenReject(conn, packet)
	case packetEchoResp:
		return e.handleEchoResp(conn, packet)
	case packetClose:
		return e.handleClose(conn, packet)
	case packetIPFrag:
		return e.handleIPFrag(conn, packet)
	case packetSEGRT, packetIPFragSR:
		e.options.Logger.Debug("drop unsupported iWAN segment routing packet")
		return false
	default:
		return false
	}
}

func (e *Endpoint) handleOpenReject(conn net.Conn, packet []byte) bool {
	if !verifyPacket(packet) {
		return false
	}
	e.access.Lock()
	if e.conn != conn || e.state != stateAuthSent {
		e.access.Unlock()
		return false
	}
	e.ready.Store(false)
	e.state = stateClosed
	e.access.Unlock()
	e.options.Logger.Error("iWAN OPEN rejected by server")
	return true
}

func (e *Endpoint) handleOpenAck(conn net.Conn, packet []byte) bool {
	info, err := parseOpenAck(packet)
	if err != nil {
		e.options.Logger.Warn(E.Cause(err, "handle iWAN OPENACK"))
		return false
	}
	prefix := netip.PrefixFrom(info.peerIP, 24)
	if len(e.options.ExpectedAddress) > 0 {
		var matched bool
		for _, expected := range e.options.ExpectedAddress {
			if expected.Contains(info.peerIP) {
				matched = true
				break
			}
		}
		if !matched {
			e.options.Logger.Warn("iWAN OPENACK assigned address ", info.peerIP, " outside configured address")
		}
	}
	effectiveMTU := e.options.MTU
	if info.serverMTU >= 68 && uint32(info.serverMTU) < effectiveMTU {
		effectiveMTU = uint32(info.serverMTU)
	}
	e.access.Lock()
	if e.conn != conn || e.state != stateAuthSent {
		e.access.Unlock()
		return false
	}
	if err = e.device.SetLocalAddress(prefix); err != nil {
		e.access.Unlock()
		e.options.Logger.Error(E.Cause(err, "configure iWAN address"))
		return false
	}
	e.device.SetMTU(effectiveMTU)
	copy(e.token[:], packet[2:4])
	copy(e.sessionID[:], packet[4:8])
	e.encrypt = info.encrypt != 0
	e.localAddress = prefix
	e.state = stateEstablished
	e.echoCounter = 0
	e.lastRecv = time.Now()
	e.ready.Store(true)
	e.access.Unlock()
	if e.options.OnAddressUpdate != nil {
		e.options.OnAddressUpdate([]netip.Prefix{prefix})
	}
	e.options.Logger.Info("iWAN established: ip=", info.peerIP, " mtu=", effectiveMTU)
	return true
}

func (e *Endpoint) handleData(conn net.Conn, packet []byte) bool {
	if len(packet) <= headerSize {
		return false
	}
	e.access.Lock()
	if e.conn != conn || !e.ready.Load() {
		e.access.Unlock()
		return false
	}
	xorKey := e.xorKey
	e.access.Unlock()
	payload := make([]byte, len(packet)-headerSize)
	copy(payload, packet[headerSize:])
	if packet[0] == packetDataEnc {
		xorData(xorKey, payload)
	}
	if err := e.device.Write(payload); err != nil {
		e.options.Logger.Error(E.Cause(err, "write iWAN data to stack"))
		return false
	}
	return true
}

func (e *Endpoint) handleEchoResp(conn net.Conn, packet []byte) bool {
	if !verifyPacket(packet) {
		return false
	}
	if len(packet) >= signedHeader+8 {
		sent := int64(binary.LittleEndian.Uint64(packet[signedHeader : signedHeader+8]))
		delay := uint32(max(time.Now().UnixMicro()-sent, int64(0)))
		e.access.Lock()
		if e.conn != conn || e.state != stateEstablished {
			e.access.Unlock()
			return false
		}
		e.curDelay = delay
		if delay > e.maxDelay {
			e.maxDelay = delay
		}
		if delay < e.minDelay {
			e.minDelay = delay
		}
		e.access.Unlock()
	}
	return true
}

func (e *Endpoint) handleClose(conn net.Conn, packet []byte) bool {
	if !verifyPacket(packet) {
		return false
	}
	e.access.Lock()
	if e.conn != conn || e.state != stateEstablished ||
		packet[2] != e.token[0] || packet[3] != e.token[1] ||
		packet[4] != e.sessionID[0] || packet[5] != e.sessionID[1] ||
		packet[6] != e.sessionID[2] || packet[7] != e.sessionID[3] {
		e.access.Unlock()
		return false
	}
	e.ready.Store(false)
	e.state = stateClosed
	e.access.Unlock()
	e.options.Logger.Info("iWAN peer closed")
	return true
}

func (e *Endpoint) writePacket(packet []byte) error {
	e.access.Lock()
	conn := e.conn
	e.access.Unlock()
	if conn == nil {
		return net.ErrClosed
	}
	return e.writePacketTo(conn, packet)
}

func (e *Endpoint) writePacketTo(conn net.Conn, packet []byte) error {
	e.writeAccess.Lock()
	defer e.writeAccess.Unlock()
	_, err := conn.Write(packet)
	return err
}

func (e *Endpoint) resetAuthLocked(now time.Time) {
	e.state = stateAuthSent
	e.token = [2]byte{}
	e.sessionID = [4]byte{}
	e.lastRecv = now
	e.lastOpen = time.Time{}
	e.echoCounter = 0
	e.minDelay = ^uint32(0)
	e.curDelay = 0
	e.maxDelay = 0
	e.localAddress = netip.Prefix{}
	e.fragmentLock.Lock()
	e.fragments = fragmentReassembler{}
	e.fragmentLock.Unlock()
}

func (e *Endpoint) markClosed(conn net.Conn) {
	e.ready.Store(false)
	e.access.Lock()
	defer e.access.Unlock()
	if e.conn == conn {
		e.conn = nil
		e.state = stateClosed
	}
}

func (e *Endpoint) isCurrentConn(conn net.Conn) bool {
	e.access.Lock()
	defer e.access.Unlock()
	return conn != nil && e.conn == conn
}

func (e *Endpoint) reconnect(now time.Time) error {
	conn, err := e.options.Dialer.DialContext(e.ctx, N.NetworkUDP, e.options.Server)
	if err != nil {
		return err
	}
	if e.ctx.Err() != nil || !e.started.Load() {
		_ = conn.Close()
		return net.ErrClosed
	}
	e.access.Lock()
	if e.ctx.Err() != nil || !e.started.Load() {
		e.access.Unlock()
		_ = conn.Close()
		return net.ErrClosed
	}
	oldConn := e.conn
	e.conn = conn
	e.resetAuthLocked(now)
	e.access.Unlock()
	if oldConn != nil {
		_ = oldConn.Close()
	}
	go e.readLoop(conn)
	return nil
}
