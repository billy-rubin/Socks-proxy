package application

import (
	"encoding/binary"
	"fmt"
	"log/slog"
	"net"
	"socks-proxy/internal/domain"
	"socks-proxy/internal/infrastructure/network" // Наш новый пакет

	"github.com/miekg/dns"
	"golang.org/x/sys/unix"
)

type ProxyService struct {
	log        *slog.Logger
	loop       domain.EventLoop
	listenerFD int
	dnsFD      int
	sessions   map[int]*domain.Session
	dnsMap     map[uint16]int // DNS ID -> Client FD
}

func NewProxyService(loop domain.EventLoop, logger *slog.Logger, port int) (*ProxyService, error) {
	lfd, err := network.ListenTCP(port)
	if err != nil {
		return nil, fmt.Errorf("failed to listen tcp: %w", err)
	}

	dfd, err := network.BindUDP()
	if err != nil {
		unix.Close(lfd)
		return nil, fmt.Errorf("failed to bind udp: %w", err)
	}

	return &ProxyService{
		log:        logger,
		loop:       loop,
		listenerFD: lfd,
		dnsFD:      dfd,
		sessions:   make(map[int]*domain.Session),
		dnsMap:     make(map[uint16]int),
	}, nil
}

func (s *ProxyService) Start() error {
	s.log.Info("Registering server sockets in EventLoop", "listener_fd", s.listenerFD, "dns_fd", s.dnsFD)

	if err := s.loop.Register(s.listenerFD, domain.EventRead); err != nil {
		return err
	}
	if err := s.loop.Register(s.dnsFD, domain.EventRead); err != nil {
		return err
	}

	s.log.Info("Proxy service is running loop...")
	return s.loop.Run(s)
}

func (s *ProxyService) HandleEvent(fd int, event domain.EventType) error {
	if fd == s.listenerFD {
		return s.acceptNewClient()
	}
	if fd == s.dnsFD {
		return s.processDNSResponse()
	}

	session := s.sessions[fd]
	if session == nil {
		return nil
	}

	switch session.State {
	case domain.StateAuth:
		return s.handshakeAuth(session)
	case domain.StateRequest:
		return s.handshakeRequest(session)
	case domain.StateConnecting:
		if fd == session.RemoteFD && (event&domain.EventWrite != 0) {
			return s.finalizeConnect(session)
		}
	case domain.StateStreaming:
		return s.pipeData(session, fd, event)
	}
	return nil
}

func (s *ProxyService) acceptNewClient() error {
	nfd, sa, err := unix.Accept(s.listenerFD)
	if err != nil {
		s.log.Error("Accept failed", "error", err)
		return err
	}

	clientIP := "unknown"
	if sockAddr, ok := sa.(*unix.SockaddrInet4); ok {
		clientIP = net.IP(sockAddr.Addr[:]).String()
	}

	unix.SetNonblock(nfd, true)

	sess := &domain.Session{
		ClientFD: nfd,
		State:    domain.StateAuth,
	}
	s.sessions[nfd] = sess

	s.log.Info("New client accepted", "fd", nfd, "ip", clientIP)
	return s.loop.Register(nfd, domain.EventRead)
}

func (s *ProxyService) handshakeAuth(sess *domain.Session) error {
	buf := make([]byte, 256)
	n, err := unix.Read(sess.ClientFD, buf)
	if err != nil || n == 0 {
		s.closeSession(sess, "auth read failed")
		return nil
	}

	_, err = unix.Write(sess.ClientFD, []byte{0x05, 0x00})
	if err != nil {
		s.closeSession(sess, "auth write failed")
		return nil
	}

	sess.State = domain.StateRequest
	s.log.Debug("Auth successful, waiting for command", "client_fd", sess.ClientFD)
	return nil
}

func (s *ProxyService) handshakeRequest(sess *domain.Session) error {
	buf := make([]byte, 1024)
	n, err := unix.Read(sess.ClientFD, buf)
	if err != nil || n < 4 {
		s.closeSession(sess, "request read failed")
		return nil
	}

	cmd := buf[1]
	if cmd != domain.CmdConnect {
		s.log.Warn("Unsupported command", "cmd", cmd)
		s.closeSession(sess, "unsupported cmd")
		return nil
	}

	atyp := buf[3]
	var addr string
	var port int

	switch atyp {
	case domain.AtypIPv4:
		addr = net.IP(buf[4:8]).String()
		port = int(binary.BigEndian.Uint16(buf[8:10]))
	case domain.AtypDomain:
		dLen := int(buf[4])
		addr = string(buf[5 : 5+dLen])
		port = int(binary.BigEndian.Uint16(buf[5+dLen : 7+dLen]))

		s.log.Info("Resolving domain", "domain", addr, "client_fd", sess.ClientFD)
		sess.TargetAddr = addr // Original domain
		sess.TargetPort = port
		sess.State = domain.StateResolving
		return s.sendDNSQuery(sess, addr)
	default:
		s.closeSession(sess, "unsupported atyp")
		return nil
	}

	sess.TargetAddr = addr
	sess.TargetPort = port

	s.log.Info("Connecting direct IP", "ip", addr, "port", port)
	return s.startTCPConnect(sess)
}

func (s *ProxyService) sendDNSQuery(sess *domain.Session, host string) error {
	m := new(dns.Msg)
	m.SetQuestion(dns.Fqdn(host), dns.TypeA)
	m.RecursionDesired = true

	m.Id = uint16(sess.ClientFD & 0xFFFF)

	packed, _ := m.Pack()

	dest := &unix.SockaddrInet4{Port: 53, Addr: [4]byte{8, 8, 8, 8}}
	err := unix.Sendto(s.dnsFD, packed, 0, dest)
	if err != nil {
		s.closeSession(sess, "dns send failed")
		return err
	}

	s.dnsMap[m.Id] = sess.ClientFD
	return nil
}

func (s *ProxyService) processDNSResponse() error {
	buf := make([]byte, 512)
	n, _, err := unix.Recvfrom(s.dnsFD, buf, 0)
	if err != nil {
		return nil
	}

	msg := new(dns.Msg)
	if err := msg.Unpack(buf[:n]); err != nil {
		s.log.Error("Failed to unpack DNS response")
		return nil
	}

	clientFD, exists := s.dnsMap[msg.Id]
	if !exists {
		return nil
	}
	delete(s.dnsMap, msg.Id)

	sess := s.sessions[clientFD]
	if sess == nil {
		return nil
	}

	var resolvedIP string
	for _, ans := range msg.Answer {
		if a, ok := ans.(*dns.A); ok {
			resolvedIP = a.A.String()
			break
		}
	}

	if resolvedIP == "" {
		s.log.Warn("DNS resolution returned no A records", "domain", sess.TargetAddr)
		s.closeSession(sess, "dns no records")
		return nil
	}

	s.log.Info("DNS Resolved", "domain", sess.TargetAddr, "ip", resolvedIP)
	sess.TargetAddr = resolvedIP
	return s.startTCPConnect(sess)
}

func (s *ProxyService) startTCPConnect(sess *domain.Session) error {
	ip := net.ParseIP(sess.TargetAddr)
	sa := &unix.SockaddrInet4{Port: sess.TargetPort}
	copy(sa.Addr[:], ip.To4())

	rfd, _ := unix.Socket(unix.AF_INET, unix.SOCK_STREAM, 0)
	unix.SetNonblock(rfd, true)

	s.log.Debug("Initiating TCP connection", "remote_ip", sess.TargetAddr, "remote_fd", rfd)

	err := unix.Connect(rfd, sa)
	if err != nil && err != unix.EINPROGRESS {
		unix.Close(rfd)
		s.closeSession(sess, "connect failed immediate")
		return nil
	}

	sess.RemoteFD = rfd
	sess.State = domain.StateConnecting
	s.sessions[rfd] = sess

	return s.loop.Register(rfd, domain.EventWrite)
}

func (s *ProxyService) finalizeConnect(sess *domain.Session) error {
	val, err := unix.GetsockoptInt(sess.RemoteFD, unix.SOL_SOCKET, unix.SO_ERROR)
	if err != nil || val != 0 {
		s.closeSession(sess, fmt.Sprintf("connect async failed: %d", val))
		return nil
	}

	s.log.Info("Connected to target", "target", sess.TargetAddr)

	resp := []byte{0x05, 0x00, 0x00, 0x01, 0, 0, 0, 0, 0, 0}
	unix.Write(sess.ClientFD, resp)

	sess.State = domain.StateStreaming

	s.loop.Modify(sess.ClientFD, domain.EventRead)
	s.loop.Modify(sess.RemoteFD, domain.EventRead)
	return nil
}

func (s *ProxyService) pipeData(sess *domain.Session, fd int, event domain.EventType) error {
	var src, dst int
	if fd == sess.ClientFD {
		src, dst = sess.ClientFD, sess.RemoteFD
	} else {
		src, dst = sess.RemoteFD, sess.ClientFD
	}

	buf := make([]byte, 8192)
	n, err := unix.Read(src, buf)
	if n > 0 {
		_, wErr := unix.Write(dst, buf[:n])
		if wErr != nil {
			s.closeSession(sess, "write error")
			return nil
		}
		s.log.Debug("Data transfer", "bytes", n, "src_fd", src)
	}

	if n == 0 || err != nil {
		s.closeSession(sess, "connection closed by peer")
	}
	return nil
}

func (s *ProxyService) closeSession(sess *domain.Session, reason string) {
	s.log.Info("Closing session", "client_fd", sess.ClientFD, "reason", reason)

	if sess.ClientFD > 0 {
		s.loop.Unregister(sess.ClientFD)
		unix.Close(sess.ClientFD)
		delete(s.sessions, sess.ClientFD)
	}
	if sess.RemoteFD > 0 {
		s.loop.Unregister(sess.RemoteFD)
		unix.Close(sess.RemoteFD)
		delete(s.sessions, sess.RemoteFD)
	}
}
