package epoll

import (
	"fmt"
	"golang.org/x/sys/unix"
	"socks-proxy/internal/domain"
)

type LinuxEventLoop struct {
	epollFD int
}

func New() (*LinuxEventLoop, error) {
	fd, err := unix.EpollCreate1(0)
	if err != nil {
		return nil, err
	}
	return &LinuxEventLoop{epollFD: fd}, nil
}

func (l *LinuxEventLoop) Register(fd int, events domain.EventType) error {
	evt := &unix.EpollEvent{
		Events: uint32(events) | unix.EPOLLET, // Edge-triggered
		Fd:     int32(fd),
	}
	return unix.EpollCtl(l.epollFD, unix.EPOLL_CTL_ADD, fd, evt)
}

func (l *LinuxEventLoop) Modify(fd int, events domain.EventType) error {
	evt := &unix.EpollEvent{
		Events: uint32(events) | unix.EPOLLET,
		Fd:     int32(fd),
	}
	return unix.EpollCtl(l.epollFD, unix.EPOLL_CTL_MOD, fd, evt)
}

func (l *LinuxEventLoop) Unregister(fd int) error {
	return unix.EpollCtl(l.epollFD, unix.EPOLL_CTL_DEL, fd, nil)
}

func (l *LinuxEventLoop) Run(handler domain.EventHandler) error {
	events := make([]unix.EpollEvent, 128)
	for {
		n, err := unix.EpollWait(l.epollFD, events, -1)
		if err != nil {
			if err == unix.EINTR {
				continue
			}
			return err
		}

		for i := 0; i < n; i++ {
			fd := int(events[i].Fd)
			evMask := events[i].Events

			var domainEv domain.EventType
			if evMask&unix.EPOLLIN != 0 {
				domainEv |= domain.EventRead
			}
			if evMask&unix.EPOLLOUT != 0 {
				domainEv |= domain.EventWrite
			}

			if err := handler.HandleEvent(fd, domainEv); err != nil {
				fmt.Printf("Error handling fd %d: %v\n", fd, err)
			}
		}
	}
}

func (l *LinuxEventLoop) Stop() {
	unix.Close(l.epollFD)
}
