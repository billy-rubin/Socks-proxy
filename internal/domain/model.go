package domain

type State int

const (
	StateAuth       State = iota // Handshake
	StateRequest                 // CONNECT request
	StateResolving               // DNS
	StateConnecting              // TCP Connect (EINPROGRESS)
	StateStreaming               // Pipe
	StateClosed                  // Closed
)

type Session struct {
	ClientFD int
	RemoteFD int
	State    State

	TargetAddr string
	TargetPort int

	ClientBuffer []byte
	RemoteBuffer []byte

	ClientWriteOffset int
	RemoteWriteOffset int
}

const (
	SocksVersion5 = 0x05
	CmdConnect    = 0x01
	AtypIPv4      = 0x01
	AtypDomain    = 0x03
)
