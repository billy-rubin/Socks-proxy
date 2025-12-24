package network

import (
	"golang.org/x/sys/unix"
)

func ListenTCP(port int) (int, error) {
	fd, err := unix.Socket(unix.AF_INET, unix.SOCK_STREAM, 0)
	if err != nil {
		return 0, err
	}

	if err := unix.SetsockoptInt(fd, unix.SOL_SOCKET, unix.SO_REUSEADDR, 1); err != nil {
		unix.Close(fd)
		return 0, err
	}

	if err := unix.SetNonblock(fd, true); err != nil {
		unix.Close(fd)
		return 0, err
	}

	addr := &unix.SockaddrInet4{Port: port}
	if err := unix.Bind(fd, addr); err != nil {
		unix.Close(fd)
		return 0, err
	}

	if err := unix.Listen(fd, 128); err != nil {
		unix.Close(fd)
		return 0, err
	}

	return fd, nil
}

func BindUDP() (int, error) {
	fd, err := unix.Socket(unix.AF_INET, unix.SOCK_DGRAM, 0)
	if err != nil {
		return 0, err
	}
	if err := unix.SetNonblock(fd, true); err != nil {
		unix.Close(fd)
		return 0, err
	}
	return fd, nil
}
