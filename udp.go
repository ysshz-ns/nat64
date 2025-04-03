package main

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"log"
	"net"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

var errNoOriginDstAddr = errors.New("cannot get original dst addr")

var bufPool = sync.Pool{
	New: func() any {
		return make([]byte, 65535)
	},
}

type ClientConn struct {
	relay     *Relay
	key       [18]byte
	dataReady *sync.Cond
	closed    bool
	msgs      [][]byte
}

func (c *ClientConn) Write(p []byte) {
	c.dataReady.L.Lock()
	if c.closed {
		c.dataReady.L.Unlock()
		return
	}
	c.msgs = append(c.msgs, p)
	c.dataReady.Signal()
	c.dataReady.L.Unlock()
}

func (c *ClientConn) Read() ([]byte, error) {
	c.dataReady.L.Lock()
	for len(c.msgs) == 0 && !c.closed {
		c.dataReady.Wait()
	}

	if c.closed {
		c.dataReady.L.Unlock()
		return nil, io.EOF
	}

	data := c.msgs[0]
	c.msgs = c.msgs[1:]
	c.dataReady.L.Unlock()

	return data, nil
}

func (c *ClientConn) Close() {
	c.dataReady.L.Lock()
	c.closed = true
	c.dataReady.Signal()
	c.dataReady.L.Unlock()
	c.relay.conntrack.Delete(c.key)
}

func (c *ClientConn) Start(src *net.UDPAddr, dst *net.UDPAddr, timeout int) {
	defer c.Close()
	conn, err := net.DialUDP("udp4", nil, dst)
	if err != nil {
		log.Println(err)
		return
	}
	defer conn.Close()

	natConn, err := c.relay.GetNATConn()
	if err != nil {
		log.Println(err)
		return
	}

	watchdog := NewWatchDog(time.Second * time.Duration(timeout))
	defer watchdog.Close()

	tx := make(chan error, 1)
	rx := make(chan error, 1)

	go udpCopy(natConn, ReadableUDPConn{conn}, src, rx, watchdog, c.relay.watchdog)
	go udpCopy(conn, c, nil, tx, watchdog, c.relay.watchdog)

	signals := []chan error{tx, rx, watchdog.Done}

	for len(signals) > 0 {
		i, e, _ := Select(signals)

		if signals[i] == watchdog.Done {
			// inactive for 60 seconds. close connection
			log.Println("connection timed out. closing connection")

			return
		}

		if e != nil {
			log.Println(e)
			return
		}

		signals = append(signals[:i], signals[i+1:]...)
	}
}

type Relay struct {
	key          [18]byte
	dstv4        *net.UDPAddr
	natConn      *net.UDPConn
	natConnError error
	natConnReady *sync.Cond

	closed    atomic.Bool
	watchdog  *WatchDog
	conntrack sync.Map
}

func (r *Relay) GetNATConn() (*net.UDPConn, error) {
	r.natConnReady.L.Lock()
	for r.natConn == nil && r.natConnError == nil {
		r.natConnReady.Wait()
	}
	r.natConnReady.L.Unlock()
	return r.natConn, r.natConnError
}

func (r *Relay) Write(src *net.UDPAddr, p []byte) {
	if r.closed.Load() {
		return
	}

	var clientConn *ClientConn

	// load or create relay
	key := makeKey(src)
	if v, ok := r.conntrack.Load(key); !ok {
		clientConn = &ClientConn{
			key:       key,
			relay:     r,
			dataReady: sync.NewCond(&sync.Mutex{}),
		}

		if actual, loaded := r.conntrack.LoadOrStore(key, clientConn); !loaded {
			go clientConn.Start(src, r.dstv4, 60)
		} else {
			clientConn = actual.(*ClientConn)
		}
	} else {
		clientConn = v.(*ClientConn)
	}

	clientConn.Write(p)
}

type ReadableUDPConn struct {
	*net.UDPConn
}

func (c ReadableUDPConn) Read() ([]byte, error) {
	buf := bufPool.Get().([]byte)
	n, err := c.UDPConn.Read(buf)
	if err != nil {
		bufPool.Put(buf)
		return nil, err
	}

	return buf[:n], nil
}

func udpCopy(dst *net.UDPConn, src interface {
	Read() ([]byte, error)
}, addr *net.UDPAddr, errCh chan error, watchdogs ...*WatchDog) {
	for {
		buf, err := src.Read()

		if err != nil {
			if err == io.EOF {
				close(errCh)
			} else {
				errCh <- err
			}
			return
		}

		for _, watchdog := range watchdogs {
			watchdog.Signal()
		}

		if addr != nil {
			_, err = dst.WriteToUDP(buf, addr)
		} else {
			_, err = dst.Write(buf)
		}

		bufPool.Put(buf[:cap(buf)])
		if err != nil {
			errCh <- err
			return
		}
	}
}

type WatchDog struct {
	active chan struct{}
	closed atomic.Bool
	Done   chan error
}

func (w *WatchDog) Signal() {
	defer func() {
		recover()
	}()

	close(w.active)
}

func (w *WatchDog) Close() {
	w.closed.Store(true)
	close(w.active)
}

func NewWatchDog(timeout time.Duration) (ret *WatchDog) {
	ret = &WatchDog{
		active: make(chan struct{}),
		Done:   make(chan error, 1),
	}

	go func() {
		for !ret.closed.Load() {
			select {
			case <-ret.active:
				ret.active = make(chan struct{})
			case <-time.After(timeout):
				ret.Done <- errors.New("timeout")
				return
			}
		}
	}()

	return
}

func (r *Relay) Close() {
	if !r.closed.CompareAndSwap(false, true) {
		return
	}

	r.watchdog.Close()
	if r.natConn != nil {
		r.natConn.Close()
	}

	r.conntrack.Range(func(key, value any) bool {
		value.(*ClientConn).Close()
		return true
	})

	relays.Delete(r.key)
}

func (r *Relay) Start(key [18]byte, src, dst *net.UDPAddr) {
	defer r.Close()
	lc := net.ListenConfig{
		Control: func(network, address string, c syscall.RawConn) (err error) {
			return Control(c, func(fd uintptr) error {
				// IP_TRANSPARENT        19
				return syscall.SetsockoptInt(int(fd), syscall.IPPROTO_IP, 19, 1)
			})
		},
	}

	if conn, err := lc.ListenPacket(context.Background(), "udp6", dst.AddrPort().String()); err != nil {
		log.Println(err)
		r.natConnReady.L.Lock()
		r.natConnError = err
		r.natConnReady.Broadcast()
		r.natConnReady.L.Unlock()
		return
	} else {
		r.natConnReady.L.Lock()
		r.natConn = conn.(*net.UDPConn)
		r.natConnReady.Broadcast()
		r.natConnReady.L.Unlock()
	}

	go func() {
		<-r.watchdog.Done
		log.Println("binding port times out. closing connection")
		r.Close()
	}()

	for {
		buf := bufPool.Get().([]byte)
		n, addr, err := r.natConn.ReadFromUDP(buf)
		if err != nil {
			bufPool.Put(buf)
			log.Println(err)
			// clean up children
			return
		}

		r.Write(addr, buf[:n])
	}
}

var relays = sync.Map{}

func createAndListenUDP(port string) (*net.UDPConn, error) {
	lc := net.ListenConfig{
		Control: func(network, address string, c syscall.RawConn) (err error) {
			return Control(c, func(fd uintptr) error {
				// IP_TRANSPARENT        19
				if e := syscall.SetsockoptInt(int(fd), syscall.IPPROTO_IP, 19, 1); e != nil {
					return e
				}

				// IPV6_RECVORIGDSTADDR    74
				if e := syscall.SetsockoptInt(int(fd), syscall.IPPROTO_IPV6, 74, 1); e != nil {
					return e
				}
				return nil
			})
		},
	}

	conn, err := lc.ListenPacket(context.Background(), "udp6", "[::1]:"+port)
	if err != nil {
		return nil, err
	}

	return conn.(*net.UDPConn), nil
}

func recvUDPMsg(conn *net.UDPConn) (src *net.UDPAddr, dst *net.UDPAddr, data []byte, err error) {
	var oob [40]byte
	buf := bufPool.Get().([]byte)

	n, oobn, _, src, err := conn.ReadMsgUDP(buf, oob[:])
	if err != nil {
		bufPool.Put(buf)
		return nil, nil, nil, err
	}

	msgs, err := syscall.ParseSocketControlMessage(oob[:oobn])
	if err != nil {
		bufPool.Put(buf)
		return nil, nil, nil, err
	}

	var origDst net.UDPAddr
	for _, msg := range msgs {
		if msg.Header.Level == syscall.IPPROTO_IPV6 && msg.Header.Type == 74 { // IPV6_RECVORIGDSTADDR = 74
			origDst.Port = int(binary.BigEndian.Uint16(msg.Data[2:]))
			origDst.IP = net.IP(msg.Data[8:])
			break
		}
	}

	if origDst.IP == nil {
		bufPool.Put(buf)
		return nil, nil, nil, errNoOriginDstAddr
	}

	return src, &origDst, buf[:n], nil
}

func makeKey(addr *net.UDPAddr) [18]byte {
	var key [18]byte
	copy(key[:], addr.IP)
	binary.BigEndian.PutUint16(key[16:], uint16(addr.Port))
	return key
}

func listenUDP(port string) (cancel func(), done chan error) {
	done = make(chan error, 1)

	conn, err := createAndListenUDP(port)
	if err != nil {
		done <- err
		return
	}

	isClosed := false

	go func() {
		for !isClosed {
			src, dst, buf, err := recvUDPMsg(conn)
			if err != nil {
				log.Println(err)
				continue
			}

			log.Println("Connecting UDP", src.String(), dst.String())

			key := makeKey(dst)

			var relay *Relay

			// load or create relay
			if r, ok := relays.Load(key); !ok {
				dstv4 := &net.UDPAddr{
					IP:   net.IPv4(dst.IP[12], dst.IP[13], dst.IP[14], dst.IP[15]),
					Port: dst.Port,
				}
				relay = &Relay{
					key:          key,
					dstv4:        dstv4,
					natConnReady: sync.NewCond(&sync.Mutex{}),
					watchdog:     NewWatchDog(time.Second * 60),
					conntrack:    sync.Map{},
				}
				if actual, loaded := relays.LoadOrStore(key, relay); !loaded {
					go relay.Start(key, src, dst)
				} else {
					relay = actual.(*Relay)
				}
			} else {
				relay = r.(*Relay)
			}

			relay.Write(src, buf)
		}
	}()

	cancel = func() {
		isClosed = true
		conn.Close()
	}

	return
}
