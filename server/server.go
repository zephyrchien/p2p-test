package main

import (
	"context"
	"fmt"
	"golang.org/x/sys/unix"
	"net"
	"sync"
	"syscall"
)

const REGISTER_TCP = 0x01
const REGISTER_UDP = 0x02

const STATUS_WAIT = 0x01
const STATUS_OK = 0x02

type peer struct {
	lock    sync.Mutex
	tcpPeer []string
	udpPeer []string
}

func (p *peer) find(t byte) string {
	p.lock.Lock()
	defer p.lock.Unlock()
	var addr string
	if t == REGISTER_TCP && len(p.tcpPeer) == 1 {
		addr = p.tcpPeer[0]
	} else if t == REGISTER_UDP && len(p.udpPeer) == 1 {
		addr = p.udpPeer[0]
	}
	return addr
}

func (p *peer) register(t byte, addr string) {
	p.lock.Lock()
	defer p.lock.Unlock()
	if t == REGISTER_TCP {
		p.tcpPeer[0] = addr
	} else if t == REGISTER_UDP {
		p.udpPeer[0] = addr
	}
}

func (p *peer) unregister(t byte) {
	p.lock.Lock()
	defer p.lock.Unlock()
	if t == REGISTER_TCP {
		p.tcpPeer = p.tcpPeer[:0]
	} else if t == REGISTER_UDP {
		p.udpPeer = p.udpPeer[:0]
	}
}

var CONTROL = func(network, address string, c syscall.RawConn) error {
	return c.Control(func(fd uintptr) {
		syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, unix.SO_REUSEADDR, 1)
		syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, unix.SO_REUSEPORT, 1)
	})
}

var Peer = &peer{
	tcpPeer: make([]string, 0, 4),
	udpPeer: make([]string, 0, 4),
}

const addr = "0.0.0.0:8888"

func main() {
	go serveTCP(addr)
	go serveUDP(addr)
	select {}
}

func serveTCP(addr string) {
	c := &net.ListenConfig{
		Control: CONTROL,
	}
	ctx := context.Background()
	lis, err := c.Listen(ctx, "tcp", addr)
	if err != nil {
		fmt.Printf("error: listen %s\n", addr)
	}
	defer lis.Close()
	fmt.Printf("tcp: listen %s\n", addr)
	for {
		conn, err := lis.Accept()
		if err != nil {
			continue
		}
		go handleTCPConn(conn)
	}
}

func serveUDP(addr string) {
	c := &net.ListenConfig{
		Control: CONTROL,
	}
	ctx := context.Background()
	conn, err := c.ListenPacket(ctx, "udp", addr)
	if err != nil {
		fmt.Printf("error: listen %s\n", addr)
	}
	fmt.Printf("udp: listen %s\n", addr)
	uconn, ok := conn.(*net.UDPConn)
	if !ok {
		fmt.Printf("error: assert udp conn\n")
	}
	buf := make([]byte, 1024)
	for {
		n, raddr, err := uconn.ReadFromUDP(buf)
		if err != nil {
			continue
		}
		fmt.Printf("udp: [%s] -> [%s]\n", raddr, uconn.LocalAddr().String())

		if err != nil || n < 9 {
			fmt.Printf("error: udp: packet size = %d\n", n)
			continue
		}

		t := buf[1]
		if buf[0] != 0x00 || (t != REGISTER_TCP || t != REGISTER_UDP) {
			fmt.Printf("error: udp: unknown cmd\n")
			continue
		}
		go handleUDPConn(t, uconn, raddr)
	}
}

func handleTCPConn(conn net.Conn) {
	/*
		switch c:=conn.(type) {
		case *net.TCPConn:
			fmt.Printf("tcp: [%s] -> [%s]\n",c.RemoteAddr().String(),c.LocalAddr().String())
		case *net.UDPConn:
			fmt.Printf("udp: [%s] -> [%s]\n",c.RemoteAddr().String(),c.LocalAddr().String())
		}
	*/
	defer conn.Close()
	buf := make([]byte, 1024)
	raddr := conn.RemoteAddr().String()
	fmt.Printf("tcp: [%s] -> [%s]\n", raddr, conn.LocalAddr().String())

	n, err := conn.Read(buf)
	if err != nil || n < 9 {
		fmt.Printf("error: tcp: packet size = %d\n", n)
		return
	}

	t := buf[1]
	if buf[0] != 0x00 || (t != REGISTER_TCP || t != REGISTER_UDP) {
		fmt.Printf("error: tcp: unknown cmd\n")
		return
	}
	fmt.Printf("tcp: [%s]: %s\n", raddr, buf[2:n])

	response := makeResponse(t, raddr)
	fmt.Printf("tcp-response: [%s]: %d %s\n", raddr, response[1], response[2:])
	conn.Write(response)
}

func handleUDPConn(t byte, conn *net.UDPConn, raddr *net.UDPAddr) {
	response := makeResponse(t, raddr.String())
	fmt.Printf("udp-response: [%s]: %d %s\n", raddr.String(), response[1], response[2:])
	conn.WriteToUDP(response, raddr)
}

func makeResponse(t byte, addr string) []byte {
	peerAddr := Peer.find(t)
	var msg string
	var stat byte
	if peerAddr == "" {
		Peer.register(t, addr)
		stat = STATUS_WAIT
		msg = "registered, wait for peer.."
	} else {
		Peer.unregister(t)
		stat = STATUS_OK
		msg = peerAddr
	}
	response := []byte{0x00, stat}
	response = append(response, []byte(msg)...)
	return response
}
