package main

import (
	"context"
	"io"
	"log"
	"net"
	"os"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"
)

func main() {
	al, _ := ParseArgs(&args)

	if args.Help {
		println("NAT64 server to relay IPv6 connections to IPv4.\n")
		println("Usage:")
		for _, arg := range al {
			println("\t" + strings.Join(arg.Aliases, ", ") + "\t" + arg.Help)
		}
		return
	}

	if args.Port == "" {
		args.Port = "8888"
	} else if p, err := strconv.Atoi(args.Port); err != nil || p < 1 || p > 65535 {
		println("Invalid port number")
		return
	}

	if args.Brutal == "" {
		args.brutal = 0
	} else if b, err := strconv.Atoi(args.Brutal); err != nil {
		println("Invalid window size")
		return
	} else {
		args.brutal = b
	}

	lc := net.ListenConfig{
		Control: func(network, address string, c syscall.RawConn) (err error) {
			return control(c, func(fd uintptr) error {
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

	l, err := lc.Listen(context.Background(), "tcp6", "[::1]:"+args.Port)
	if err != nil {
		log.Panic(err)
	}

	for {
		conn, err := l.Accept()
		if err != nil {
			log.Panic(err)
		}

		go proxy(conn.(*net.TCPConn))
	}
}

func proxy(conn *net.TCPConn) {
	defer conn.Close()

	if args.brutal > 0 {
		// enable tcp-brutal
		// tcp-brutal implements a custom congestion control algorithms when the default is not suitable.
		// this should only be used if you know what you are doing.

		if sc, err := conn.SyscallConn(); err != nil {
			log.Println(err)
			return
		} else if err := control(sc, func(fd uintptr) error {
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

	wg := &sync.WaitGroup{}
	wg.Add(2)
	go copy(conn, remote, wg)
	go copy(remote, conn, wg)
	wg.Wait()
}

// network utils

func copy(dst *net.TCPConn, src *net.TCPConn, wg *sync.WaitGroup) {
	defer wg.Done()
	if _, err := io.Copy(dst, src); err == nil || err == io.EOF {
		dst.CloseWrite()
	} else {
		log.Println(err)
		// force timeout to prevent lingering connections
		now := time.Now()
		src.SetDeadline(now)
		dst.SetDeadline(now)
	}
}

type TCPBrutalParams struct {
	Rate     uint64
	CwndGain uint32
}

//go:linkname setsockopt syscall.setsockopt
func setsockopt(s int, level int, name int, val unsafe.Pointer, vallen uintptr) (err error)

func control(sc syscall.RawConn, f func(fd uintptr) error) (err error) {
	if e := sc.Control(func(fd uintptr) {
		err = f(fd)
	}); e != nil {
		err = e
	}
	return
}

// command line utils

var args Args

// Args is the struct containing the command line arguments.
// It supports 3 types of arguments:
// - Flags: arguments that do not take a value, e.g. --help. They are represented by a boolean field.
// - Options: arguments that take a value, e.g. --port 8080. They are represented by a string.
// - Multiple values: arguments that can be specified multiple times, e.g. --file file1 --file file2. They are represented by a slice of strings.
type Args struct {
	Port   string `arg:"-p,--port" help:"Port to listen on. Default port is 8888."`
	Brutal string `arg:"-w,--window" help:"TCP window size. Requires tcp-brutal kernel module."`
	Help   bool   `arg:"-h,--help" help:"Show this help message"`

	brutal int
}

type ArgListItem struct {
	Aliases  []string
	Help     string
	field    reflect.Value
	HasValue bool
	isSlice  bool
	isBool   bool
}

// ParseArgs is a minimal parser for command line arguments.
func ParseArgs[T any](args *T) (argList []ArgListItem, rest []string) {
	value := reflect.ValueOf(args).Elem()
	typ := value.Type()

	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)
		if field.Tag == "" {
			continue
		}

		kind := field.Type.Kind()
		if kind != reflect.Bool && kind != reflect.String && kind != reflect.Slice {
			log.Panic("Unsupported arg type: " + field.Type.String())
		}

		argList = append(argList, ArgListItem{
			Aliases: strings.Split(field.Tag.Get("arg"), ","),
			Help:    field.Tag.Get("help"),
			field:   value.Field(i),
			isSlice: kind == reflect.Slice,
			isBool:  kind == reflect.Bool,
		})
	}

	commandline := os.Args[1:]
	for i := 0; i < len(commandline); {
		// not performance critical, so we can use a simple loop
		matched := false
		for _, arg := range argList {
			if arg.HasValue && !arg.isSlice {
				continue
			}

			for _, alias := range arg.Aliases {
				if commandline[i] == alias {
					if arg.isBool {
						arg.field.SetBool(true)
						arg.HasValue = true
						i++
						break
					}

					if arg.isSlice {
						arg.field.Set(reflect.Append(arg.field, reflect.ValueOf(commandline[i+1])))
					} else {
						arg.field.Set(reflect.ValueOf(commandline[i+1]))
					}

					arg.HasValue = true
					i += 2
					break
				}
			}

			if arg.HasValue {
				matched = true
				break
			}
		}

		if matched {
			continue
		}

		if strings.HasPrefix(commandline[i], "-") {
			// unrecognized argument. ignore it
		} else {
			// positional argument
			rest = append(rest, commandline[i])
			i++
		}
	}
	return
}
