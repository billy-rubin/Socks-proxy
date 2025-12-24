package domain

type EventType uint32

const (
	EventRead  EventType = 0x1
	EventWrite EventType = 0x4 // EPOLLOUT
)

type EventHandler interface {
	HandleEvent(fd int, event EventType) error
}

type EventLoop interface {
	Register(fd int, events EventType) error
	Modify(fd int, events EventType) error
	Unregister(fd int) error
	Run(handler EventHandler) error
	Stop()
}

type DNSResolver interface {
	Resolve(domain string, sessionID int) error
}
