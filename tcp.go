package main

import (
	"context"
	"io"
	"log"
	"net"
	"syscall"
	"time"
	"unsafe"
)

func tcpCopy(dst *net.TCPConn, src *net.TCPConn, result *Task[error]) {
	_, err := io.Copy(dst, src)
	if err == nil {
		dst.CloseWrite()
	} else {
		// force timeout to prevent lingering connections
		now := time.Now()
		src.SetDeadline(now)
		dst.SetDeadline(now)
	}

	if result != nil {
		result.Resolve(err)
	}
}

type TCPBrutalParams struct {
	Rate     uint64
	CwndGain uint32
}

//go:linkname setsockopt syscall.setsockopt
func setsockopt(s int, level int, name int, val unsafe.Pointer, vallen uintptr) (err error)

func proxy(conn *net.TCPConn) {
	defer conn.Close()

	if args.brutal > 0 {
		// enable tcp-brutal
		// tcp-brutal implements a custom congestion control algorithms when the default is not suitable.
		// this should only be used if you know what you are doing.

		if sc, err := conn.SyscallConn(); err != nil {
			log.Println(err)
			return
		} else if err := Control(sc, func(fd uintptr) error {
			// TCP_CONGESTION         13
			if e := syscall.SetsockoptString(int(fd), syscall.IPPROTO_TCP, 13, "brutal"); e != nil {
				return e
			}

			params := TCPBrutalParams{
				Rate:     uint64(args.brutal),
				CwndGain: 20, // hysteria2 default
			}

			// TCP_BRUTAL_PARAMS      23301
			if e := setsockopt(int(fd), syscall.IPPROTO_TCP, 23301, unsafe.Pointer(&params), unsafe.Sizeof(params)); e != nil {
				return e
			}
			return nil
		}); err != nil {
			log.Println(err)
			return
		}
	}

	addr := conn.LocalAddr().(*net.TCPAddr)
	targetAddr := net.IPv4(addr.IP[12], addr.IP[13], addr.IP[14], addr.IP[15])
	log.Println("Connecting to", targetAddr.String(), addr.Port)
	remote, err := net.DialTCP("tcp4", nil, &net.TCPAddr{IP: targetAddr, Port: addr.Port})

	if err != nil {
		log.Println(err)
		return
	}

	defer remote.Close()
	result := NewTask[error]()
	go tcpCopy(remote, conn, result)
	tcpCopy(conn, remote, nil)
	if err := result.GetResult(); err != nil {
		log.Println(err)
	}
}

func listenTCP(port string) (cancel func(), done chan error) {
	done = make(chan error, 1)
	lc := net.ListenConfig{
		Control: func(network, address string, c syscall.RawConn) (err error) {
			return Control(c, func(fd uintptr) error {
				// IP_TRANSPARENT        19
				if e := syscall.SetsockoptInt(int(fd), syscall.IPPROTO_IP, 19, 1); e != nil {
					return e
				}

				if e := syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_REUSEADDR, 1); e != nil {
					return e
				}
				return nil
			})
		},
	}

	l, err := lc.Listen(context.Background(), "tcp6", "[::1]:"+port)
	if err != nil {
		done <- err
		return
	}

	go func() {
		for {
			conn, err := l.Accept()
			if err != nil {
				done <- err
				return
			}

			go proxy(conn.(*net.TCPConn))
		}
	}()

	cancel = func() {
		l.Close()
	}

	return
}
