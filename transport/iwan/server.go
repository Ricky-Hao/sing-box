//go:build with_gvisor

package iwan

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/binary"
	"net"
	"net/netip"
	"sync"
	"sync/atomic"
	"time"

	"github.com/sagernet/sing-tun"
	E "github.com/sagernet/sing/common/exceptions"
	"github.com/sagernet/sing/common/logger"
	M "github.com/sagernet/sing/common/metadata"
)

type ServerOptions struct {
	Context        context.Context
	Logger         logger.ContextLogger
	Handler        tun.Handler
	UDPTimeout     time.Duration
	ICMPTimeout    time.Duration
	AddressPool    netip.Prefix
	Users          []ServerUser
	MTU            uint32
	Encrypt        bool
	DNS            []netip.Addr
	SessionTimeout time.Duration
}

type ServerUser struct {
	Username string
	Password string
	Address  netip.Addr
}

type Server struct {
	options ServerOptions
	ctx     context.Context
	cancel  context.CancelFunc

	device *stackDevice
	conn   net.PacketConn

	started     atomic.Bool
	access      sync.Mutex
	writeAccess sync.Mutex
	users       map[string]serverUser
	byRemote    map[netip.AddrPort]*serverSession
	byAddress   map[netip.Addr]*serverSession
	byUsername  map[string]*serverSession
	addressUser map[netip.Addr]string
	poolBase    uint32
}

type serverUser struct {
	password [16]byte
	xorKey   [8]byte
	address  netip.Addr
}

type serverSession struct {
	username  string
	remote    netip.AddrPort
	address   netip.Addr
	token     [2]byte
	sessionID [4]byte
	xorKey    [8]byte
	encrypt   bool
	lastRecv  time.Time
	fragments fragmentReassembler
}

func NewServer(options ServerOptions) (*Server, error) {
	if options.MTU == 0 {
		options.MTU = defaultMTU
	}
	if options.MTU < minMTU || options.MTU > maxMTU {
		return nil, E.New("invalid MTU: ", options.MTU, ", required ", minMTU, "-", maxMTU)
	}
	if options.SessionTimeout == 0 {
		options.SessionTimeout = dataTimeout
	}
	if options.UDPTimeout == 0 {
		options.UDPTimeout = dataTimeout
	}
	if !options.AddressPool.IsValid() || !options.AddressPool.Addr().Is4() || options.AddressPool.Bits() != 24 {
		return nil, E.New("iWAN server address_pool must be an IPv4 /24 prefix")
	}
	if len(options.Users) == 0 {
		return nil, E.New("missing users")
	}
	ctx, cancel := context.WithCancel(options.Context)
	options.Context = ctx
	server := &Server{
		options:     options,
		ctx:         ctx,
		cancel:      cancel,
		users:       make(map[string]serverUser),
		byRemote:    make(map[netip.AddrPort]*serverSession),
		byAddress:   make(map[netip.Addr]*serverSession),
		byUsername:  make(map[string]*serverSession),
		addressUser: make(map[netip.Addr]string),
		poolBase:    ipv4ToUint32(options.AddressPool.Masked().Addr()),
	}
	userOrder := make([]string, 0, len(options.Users))
	for _, user := range options.Users {
		if user.Username == "" {
			cancel()
			return nil, E.New("missing username")
		}
		if user.Password == "" {
			cancel()
			return nil, E.New("missing password for user ", user.Username)
		}
		if _, exists := server.users[user.Username]; exists {
			cancel()
			return nil, E.New("duplicate user: ", user.Username)
		}
		if user.Address.IsValid() {
			if !user.Address.Is4() || !options.AddressPool.Contains(user.Address) || user.Address == options.AddressPool.Addr() {
				cancel()
				return nil, E.New("invalid address for user ", user.Username, ": ", user.Address)
			}
			host := ipv4ToUint32(user.Address) - server.poolBase
			if host < 2 || host > 254 {
				cancel()
				return nil, E.New("invalid address for user ", user.Username, ": ", user.Address)
			}
			if owner, exists := server.addressUser[user.Address]; exists {
				cancel()
				return nil, E.New("duplicate static iWAN address ", user.Address, " for users ", owner, " and ", user.Username)
			}
			server.addressUser[user.Address] = user.Username
		}
		passwordBlock, err := encryptedPassword(user.Username, user.Password)
		if err != nil {
			cancel()
			return nil, err
		}
		server.users[user.Username] = serverUser{
			password: passwordBlock,
			xorKey:   deriveXORKey(user.Username, user.Password),
			address:  user.Address,
		}
		userOrder = append(userOrder, user.Username)
	}
	for _, username := range userOrder {
		user := server.users[username]
		if user.address.IsValid() {
			continue
		}
		address, ok := server.allocateUnusedAddress(username)
		if !ok {
			cancel()
			return nil, E.New("iWAN server address_pool exhausted")
		}
		user.address = address
		server.users[username] = user
	}
	device, err := newStackDevice(EndpointOptions{
		Context:     ctx,
		Logger:      options.Logger,
		Handler:     options.Handler,
		UDPTimeout:  options.UDPTimeout,
		ICMPTimeout: options.ICMPTimeout,
		MTU:         options.MTU,
	})
	if err != nil {
		cancel()
		return nil, err
	}
	server.device = device
	return server, nil
}

func (s *Server) Start(conn net.PacketConn) error {
	if s.started.Swap(true) {
		return nil
	}
	setUDPSocketBuffer(s.options.Logger, conn)
	s.access.Lock()
	s.conn = conn
	s.access.Unlock()
	go s.readLoop(conn)
	go s.deviceLoop()
	go s.timerLoop()
	return nil
}

func (s *Server) Close() error {
	s.started.Store(false)
	s.cancel()
	s.access.Lock()
	conn := s.conn
	s.conn = nil
	s.access.Unlock()
	if conn != nil {
		_ = conn.Close()
	}
	return s.device.Close()
}

func (s *Server) UserByAddress(address netip.Addr) string {
	s.access.Lock()
	defer s.access.Unlock()
	session := s.byAddress[address]
	if session == nil {
		return ""
	}
	return session.username
}

func (s *Server) readLoop(conn net.PacketConn) {
	buffer := make([]byte, fragmentOutputSize)
	for {
		n, addr, err := conn.ReadFrom(buffer)
		if err != nil {
			select {
			case <-s.ctx.Done():
				return
			default:
			}
			s.options.Logger.Error(E.Cause(err, "read iWAN server packet"))
			return
		}
		remote := M.SocksaddrFromNet(addr).Unwrap().AddrPort()
		packet := make([]byte, n)
		copy(packet, buffer[:n])
		if s.handlePacket(conn, remote, packet) {
			s.access.Lock()
			if session := s.byRemote[remote]; session != nil {
				session.lastRecv = time.Now()
			}
			s.access.Unlock()
		}
	}
}

func (s *Server) deviceLoop() {
	buffer := make([]byte, fragmentOutputSize)
	for {
		n, err := s.device.Read(buffer)
		if err != nil {
			select {
			case <-s.ctx.Done():
				return
			default:
			}
			s.options.Logger.Error(E.Cause(err, "read iWAN server stack packet"))
			return
		}
		destination, ok := ipv4Destination(buffer[:n])
		if !ok {
			continue
		}
		s.access.Lock()
		session := s.byAddress[destination]
		if session == nil {
			s.access.Unlock()
			s.options.Logger.Debug("drop iWAN server packet to unknown tunnel address ", destination)
			continue
		}
		remote := session.remote
		packet := wrapDataPacket(session, buffer[:n])
		conn := s.conn
		s.access.Unlock()
		if conn == nil {
			continue
		}
		if err = s.writePacketTo(conn, remote, packet); err != nil {
			s.options.Logger.Error(E.Cause(err, "write iWAN server data packet"))
		}
	}
}

func (s *Server) timerLoop() {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-s.ctx.Done():
			return
		case now := <-ticker.C:
			s.expireSessions(now)
		}
	}
}

func (s *Server) expireSessions(now time.Time) {
	s.access.Lock()
	defer s.access.Unlock()
	for remote, session := range s.byRemote {
		if now.Sub(session.lastRecv) <= s.options.SessionTimeout {
			continue
		}
		s.deleteSessionLocked(session)
		delete(s.byRemote, remote)
	}
}

func (s *Server) handlePacket(conn net.PacketConn, remote netip.AddrPort, packet []byte) bool {
	if len(packet) < headerSize {
		return false
	}
	switch packet[0] {
	case packetOpen:
		return s.handleOpen(conn, remote, packet)
	case packetData, packetDataEnc:
		return s.handleData(remote, packet)
	case packetIPFrag:
		return s.handleIPFrag(remote, packet)
	case packetEchoReq:
		return s.handleEcho(conn, remote, packet)
	case packetClose:
		return s.handleClose(remote, packet)
	case packetSEGRT, packetIPFragSR:
		s.options.Logger.Debug("drop unsupported iWAN segment routing packet")
		return false
	default:
		return false
	}
}

func (s *Server) handleOpen(conn net.PacketConn, remote netip.AddrPort, packet []byte) bool {
	info, err := parseOpenPacket(packet)
	if err != nil {
		s.options.Logger.Warn(E.Cause(err, "handle iWAN OPEN"))
		_ = s.writePacketTo(conn, remote, buildOpenRejectPacket())
		return false
	}
	s.access.Lock()
	user, ok := s.users[info.username]
	if !ok || subtle.ConstantTimeCompare(info.passwordBlock[:], user.password[:]) != 1 {
		s.access.Unlock()
		_ = s.writePacketTo(conn, remote, buildOpenRejectPacket())
		return false
	}
	if existing := s.byRemote[remote]; existing != nil && existing.username != info.username {
		s.access.Unlock()
		_ = s.writePacketTo(conn, remote, buildOpenRejectPacket())
		return false
	}
	if existing := s.byUsername[info.username]; existing != nil {
		if existing.remote != remote {
			delete(s.byRemote, existing.remote)
			existing.remote = remote
			s.byRemote[remote] = existing
		}
		if _, err = rand.Read(existing.token[:]); err != nil {
			s.access.Unlock()
			_ = s.writePacketTo(conn, remote, buildOpenRejectPacket())
			return false
		}
		if _, err = rand.Read(existing.sessionID[:]); err != nil {
			s.access.Unlock()
			_ = s.writePacketTo(conn, remote, buildOpenRejectPacket())
			return false
		}
		existing.lastRecv = time.Now()
		packet = buildOpenAckPacket(existing.token, existing.sessionID, min(s.options.MTU, uint32(info.mtu)), existing.address, existing.encrypt, s.options.DNS)
		s.access.Unlock()
		return s.writePacketTo(conn, remote, packet) == nil
	}
	address, ok := s.allocateAddressLocked(user)
	if !ok {
		s.access.Unlock()
		_ = s.writePacketTo(conn, remote, buildOpenRejectPacket())
		return false
	}
	session := &serverSession{
		username: info.username,
		remote:   remote,
		address:  address,
		xorKey:   user.xorKey,
		encrypt:  s.options.Encrypt,
		lastRecv: time.Now(),
	}
	if _, err = rand.Read(session.token[:]); err != nil {
		s.access.Unlock()
		_ = s.writePacketTo(conn, remote, buildOpenRejectPacket())
		return false
	}
	if _, err = rand.Read(session.sessionID[:]); err != nil {
		s.access.Unlock()
		_ = s.writePacketTo(conn, remote, buildOpenRejectPacket())
		return false
	}
	s.byRemote[remote] = session
	s.byAddress[address] = session
	s.byUsername[info.username] = session
	packet = buildOpenAckPacket(session.token, session.sessionID, min(s.options.MTU, uint32(info.mtu)), address, session.encrypt, s.options.DNS)
	s.access.Unlock()
	return s.writePacketTo(conn, remote, packet) == nil
}

func (s *Server) handleData(remote netip.AddrPort, packet []byte) bool {
	if len(packet) <= headerSize {
		return false
	}
	s.access.Lock()
	session := s.byRemote[remote]
	if session == nil || !session.matchHeader(packet) || session.encrypt != (packet[0] == packetDataEnc) {
		s.access.Unlock()
		return false
	}
	payload := make([]byte, len(packet)-headerSize)
	copy(payload, packet[headerSize:])
	if session.encrypt {
		xorData(session.xorKey, payload)
	}
	if !validSessionSource(payload, session.address) {
		s.access.Unlock()
		return false
	}
	s.access.Unlock()
	if err := s.device.Write(payload); err != nil {
		s.options.Logger.Error(E.Cause(err, "write iWAN server data to stack"))
		return false
	}
	return true
}

func (s *Server) handleIPFrag(remote netip.AddrPort, packet []byte) bool {
	s.access.Lock()
	session := s.byRemote[remote]
	if session == nil || !session.matchHeader(packet) {
		s.access.Unlock()
		return false
	}
	payload, ok := session.fragments.handle(packet)
	if !ok {
		s.access.Unlock()
		return false
	}
	if payload == nil {
		s.access.Unlock()
		return true
	}
	if session.encrypt {
		xorData(session.xorKey, payload)
	}
	if !validSessionSource(payload, session.address) {
		s.access.Unlock()
		return false
	}
	s.access.Unlock()
	if err := s.device.Write(payload); err != nil {
		s.options.Logger.Error(E.Cause(err, "write iWAN server fragment to stack"))
		return false
	}
	return true
}

func (s *Server) handleEcho(conn net.PacketConn, remote netip.AddrPort, packet []byte) bool {
	if !verifyPacket(packet) {
		return false
	}
	s.access.Lock()
	session := s.byRemote[remote]
	if session == nil || !session.matchHeader(packet) {
		s.access.Unlock()
		return false
	}
	response := buildEchoResponsePacket(packet)
	s.access.Unlock()
	return s.writePacketTo(conn, remote, response) == nil
}

func (s *Server) handleClose(remote netip.AddrPort, packet []byte) bool {
	if !verifyPacket(packet) {
		return false
	}
	s.access.Lock()
	defer s.access.Unlock()
	session := s.byRemote[remote]
	if session == nil || !session.matchHeader(packet) {
		return false
	}
	s.deleteSessionLocked(session)
	return true
}

func (s *Server) allocateAddressLocked(user serverUser) (netip.Addr, bool) {
	if user.address.IsValid() {
		if s.byAddress[user.address] != nil {
			return netip.Addr{}, false
		}
		return user.address, true
	}
	return netip.Addr{}, false
}

func (s *Server) allocateUnusedAddress(username string) (netip.Addr, bool) {
	for host := uint32(2); host <= 254; host++ {
		address := uint32ToIPv4(s.poolBase + host)
		if _, reserved := s.addressUser[address]; !reserved {
			s.addressUser[address] = username
			return address, true
		}
	}
	return netip.Addr{}, false
}

func (s *Server) deleteSessionLocked(session *serverSession) {
	delete(s.byRemote, session.remote)
	delete(s.byAddress, session.address)
	delete(s.byUsername, session.username)
}

func (s *Server) writePacketTo(conn net.PacketConn, remote netip.AddrPort, packet []byte) error {
	s.writeAccess.Lock()
	defer s.writeAccess.Unlock()
	_, err := conn.WriteTo(packet, M.SocksaddrFromNetIP(remote).UDPAddr())
	return err
}

func (s *serverSession) matchHeader(packet []byte) bool {
	return len(packet) >= headerSize &&
		packet[2] == s.token[0] && packet[3] == s.token[1] &&
		packet[4] == s.sessionID[0] && packet[5] == s.sessionID[1] &&
		packet[6] == s.sessionID[2] && packet[7] == s.sessionID[3]
}

func wrapDataPacket(session *serverSession, payload []byte) []byte {
	packet := make([]byte, headerSize+len(payload))
	if session.encrypt {
		packet[0] = packetDataEnc
		packet[1] = 1
		copy(packet[headerSize:], payload)
		xorData(session.xorKey, packet[headerSize:])
	} else {
		packet[0] = packetData
		copy(packet[headerSize:], payload)
	}
	copy(packet[2:4], session.token[:])
	copy(packet[4:8], session.sessionID[:])
	return packet
}

func validSessionSource(packet []byte, expected netip.Addr) bool {
	source, _, ok := ipv4SourceDestination(packet)
	return ok && source == expected
}

func ipv4Destination(packet []byte) (netip.Addr, bool) {
	_, destination, ok := ipv4SourceDestination(packet)
	return destination, ok
}

func ipv4SourceDestination(packet []byte) (netip.Addr, netip.Addr, bool) {
	if len(packet) < 20 || packet[0]>>4 != 4 {
		return netip.Addr{}, netip.Addr{}, false
	}
	headerLength := int(packet[0]&0x0f) * 4
	if headerLength < 20 || len(packet) < headerLength {
		return netip.Addr{}, netip.Addr{}, false
	}
	source := netip.AddrFrom4([4]byte(packet[12:16]))
	destination := netip.AddrFrom4([4]byte(packet[16:20]))
	return source, destination, true
}

func ipv4ToUint32(address netip.Addr) uint32 {
	bytes := address.As4()
	return binary.BigEndian.Uint32(bytes[:])
}

func uint32ToIPv4(address uint32) netip.Addr {
	var bytes [4]byte
	binary.BigEndian.PutUint32(bytes[:], address)
	return netip.AddrFrom4(bytes)
}
