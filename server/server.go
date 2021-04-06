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

const CLIENT_FIRST = 0x01
const CLIENT_SECOND = 0x02


type peer struct {
	lock    sync.Mutex
	tcpPeer []*net.TCPConn
	udpPeer []*net.UDPAddr
}

func (p *peer)findTCP() *net.TCPConn{
	p.lock.Lock()
	defer p.lock.Unlock()
	if len(p.tcpPeer)==1 {
		return p.tcpPeer[0]
	}
	return nil
}

func (p *peer) findUDP() *net.UDPAddr {
	p.lock.Lock()
	defer p.lock.Unlock()
	if len(p.udpPeer) == 1 {
		return p.udpPeer[0]
	}
	return nil
}


func (p *peer) registerTCP(conn *net.TCPConn) {
	p.lock.Lock()
	defer p.lock.Unlock()
	p.tcpPeer= append(p.tcpPeer,conn)
}

func (p *peer)registerUDP(addr *net.UDPAddr){
	p.lock.Lock()
	defer p.lock.Unlock()
	p.udpPeer=append(p.udpPeer,addr)
}

func (p *peer) unregisterTCP() {
	p.lock.Lock()
	defer p.lock.Unlock()
	/*
	for _,conn:=range p.tcpPeer{
		conn.Close()
	}*/
	p.tcpPeer = p.tcpPeer[:0]
}

func (p *peer) unregisterUDP() {
	p.lock.Lock()
	defer p.lock.Unlock()
	p.udpPeer = p.udpPeer[:0]
}

var CONTROL = func(network, address string, c syscall.RawConn) error {
	return c.Control(func(fd uintptr) {
		syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, unix.SO_REUSEADDR, 1)
		syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, unix.SO_REUSEPORT, 1)
	})
}

var Peer = &peer{
	tcpPeer: make([]*net.TCPConn, 0,4),
	udpPeer: make([]*net.UDPAddr, 0,4),
}

const addr = "0.0.0.0:8388"

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
		tcpConn,ok:=conn.(*net.TCPConn)
		if !ok{
			continue
		}
		go handleTCPConn(tcpConn)
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

		if err != nil || n < 3 {
			fmt.Printf("error: udp: packet size = %d\n", n)
			continue
		}

		t := buf[1]
		if buf[0] != 0x00 || (t != REGISTER_TCP && t != REGISTER_UDP) {
			fmt.Printf("error: udp: unknown cmd\n")
			continue
		}
		go handleUDPConn(uconn, raddr)
	}
}

func handleTCPConn(conn *net.TCPConn) {
	/*
		switch c:=conn.(type) {
		case *net.TCPConn:
			fmt.Printf("tcp: [%s] -> [%s]\n",c.RemoteAddr().String(),c.LocalAddr().String())
		case *net.UDPConn:
			fmt.Printf("udp: [%s] -> [%s]\n",c.RemoteAddr().String(),c.LocalAddr().String())
		}
	*/
	buf := make([]byte, 1024)
	raddr := conn.RemoteAddr().String()
	fmt.Printf("tcp: [%s] -> [%s]\n", raddr, conn.LocalAddr().String())

	n, err := conn.Read(buf)
	if err != nil || n < 3 {
		fmt.Printf("error: tcp: packet size = %d\n", n)
		return
	}

	t := buf[1]
	if buf[0] != 0x00 || (t != REGISTER_TCP && t != REGISTER_UDP) {
		fmt.Printf("error: tcp: unknown cmd\n")
		return
	}

	peerConn := Peer.findTCP()
	if peerConn == nil {
		Peer.registerTCP(conn)
		msg := "registered, wait for peer.."
		response:=append([]byte{0x00,STATUS_WAIT},msg...)
		conn.Write(response)
		fmt.Printf("tcp-response: [%s]: %d %s\n", raddr, response[1], response[2:])
	} else if peerConn.RemoteAddr().String() == raddr {
		msg := "registered, wait for peer.."
		response:=append([]byte{0x00,STATUS_WAIT},msg...)
		conn.Write(response)
		fmt.Printf("tcp-response: [%s]: %d %s\n", raddr, response[1], response[2:])
	} else {
		Peer.unregisterTCP()
		response:=append([]byte{0x00,STATUS_OK},peerConn.RemoteAddr().String()...)
		conn.Write(response)
		fmt.Printf("tcp-response: [%s]: %d %s\n", raddr, response[1], response[2:])

		notice:=append([]byte{0x00,STATUS_OK},raddr...)
		peerConn.Write(notice)
		fmt.Printf("tcp-notice: [%s]: %d %s\n", peerConn.RemoteAddr().String(), notice[1], notice[2:])
	}
}

func handleUDPConn(conn *net.UDPConn, raddr *net.UDPAddr) {
	peerAddr := Peer.findUDP()
	addr:=raddr.String()
	if peerAddr == nil {
		Peer.registerUDP(raddr)
		msg := "registered, wait for peer.."
		response:=append([]byte{0x00,STATUS_WAIT,CLIENT_FIRST},msg...)
		conn.WriteToUDP(response, raddr)
		fmt.Printf("udp-response: [%s]: %d %s\n", addr, response[1], response[2:])
	} else if peerAddr.String() == addr {
		msg := "registered, wait for peer.."
		response:=append([]byte{0x00,STATUS_WAIT,CLIENT_FIRST},msg...)
		conn.WriteToUDP(response, raddr)
		fmt.Printf("udp-response: [%s]: %d %s\n", addr, response[1], response[2:])
	} else {
		Peer.unregisterUDP()
		response:=append([]byte{0x00,STATUS_OK,CLIENT_SECOND},peerAddr.String()...)
		conn.WriteToUDP(response, raddr)
		fmt.Printf("udp-response: [%s]: %d %s\n", addr, response[1], response[2:])

		notice:=append([]byte{0x00,STATUS_OK,CLIENT_FIRST},addr...)
		conn.WriteToUDP(notice,peerAddr)
		fmt.Printf("udp-notice: [%s]: %d %s\n", peerAddr.String(), notice[1], notice[2:])
	}
}
