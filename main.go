package main

import (
	"context"
	"log"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
)

func main() {
	al, _ := ParseArgs(&args)

	if args.TcpPort == "" {
		args.TcpPort = "8888"
	} else if p, err := strconv.Atoi(args.TcpPort); err != nil {
		println("Invalid port number")
		return
	} else if p == 0 {
		args.TcpPort = ""
	} else if p < 1 || p > 65535 {
		println("Invalid port number")
		return
	}

	if args.UdpPort == "" {
		args.UdpPort = "8888"
	} else if p, err := strconv.Atoi(args.UdpPort); err != nil {
		println("Invalid port number")
		return
	} else if p == 0 {
		args.UdpPort = ""
	} else if p < 1 || p > 65535 {
		println("Invalid port number")
		return
	}

	if args.Help || (args.TcpPort == "" && args.UdpPort == "") {
		println("NAT64 server to relay IPv6 connections to IPv4.\n")
		println("Usage:")
		for _, arg := range al {
			println("\t" + strings.Join(arg.Aliases, ", ") + "\t" + arg.Help)
		}
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

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM) // alloc
	defer cancel()

	tcpCancel := func() {}
	tcpDone := make(chan error, 1)

	if args.TcpPort != "" {
		tcpCancel, tcpDone = listenTCP(args.TcpPort)
	}

	udpCancel := tcpCancel
	udpDone := tcpDone
	if args.UdpPort != "" {
		udpCancel, udpDone = listenUDP(args.UdpPort)
	}

	if err, _, _ := GetChannelValue(tcpDone); err != nil {
		log.Panic(err)
	}

	if err, _, _ := GetChannelValue(udpDone); err != nil {
		log.Panic(err)
	}

	select {
	case <-ctx.Done():
		log.Println("Shutting down...")
		tcpCancel()
		udpCancel()
	case err := <-tcpDone:
		if err != nil {
			log.Panic(err)
		}
	case err := <-udpDone:
		if err != nil {
			log.Panic(err)
		}
	}
}
