package main

import (
	"context"
	"flag"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"
)

var (
	PunchFrom bool
	PunchTo   bool
	PeerUDP	  *net.UDPAddr
	Network   = flag.String("net", "udp", "")
	LADDR     = flag.String("l", ":8388", "listen addr")
	RADDR     = flag.String("s", "", "remote addr")
)

const DISPATCHER = 0x00
const PEER = 0x01
const REGISTER_TCP = 0x01
const REGISTER_UDP = 0x02
const STATUS_WAIT = 0x01
const STATUS_OK = 0x02
const DIRECTION_FROM = 0x00
const DIRECTION_TO = 0x01

func init() {
	flag.Parse()
}

func main() {
	if *RADDR == "" {
		fmt.Printf("no remote address specified, quit\n")
		return
	}
	if *Network == "tcp" {
		serveTCP()
	}
	if *Network == "udp" {
		serveUDP()
	}
}

func serveTCP() {
	lisc := &net.ListenConfig{
		Control: CONTROL,
	}
	lis, err := lisc.Listen(context.Background(), "tcp", *LADDR)
	if err != nil {
		fmt.Printf("tcp::error: listen %s\n", *LADDR)
	}
	defer lis.Close()
	fmt.Printf("tcp: listen %s\n", *LADDR)

	// dispatcher
	port, _ := strconv.Atoi(strings.Replace(*LADDR, ":", "", 1))
	tcpAddr := &net.TCPAddr{Port: port}
	dialer := net.Dialer{
		LocalAddr: tcpAddr,
		Control:   CONTROL,
	}
	conn, err := dialer.Dial("tcp", *RADDR)
	if err != nil {
		fmt.Printf("tcp::error: %s\n", err)
		return
	}
	defer conn.Close()
	wbuf := []byte{DISPATCHER, REGISTER_TCP, 0x20}
	if _, err := conn.Write(wbuf); err != nil {
		fmt.Printf("tcp::error: %s\n", err)
		return
	}
	rbuf := make([]byte, 1024)
	var peerAddr string
	for {
		n, err := conn.Read(rbuf)
		if err != nil {
			fmt.Printf("tcp::error: %s\n", err)
			return
		}
		if n < 3 || rbuf[0] != DISPATCHER {
			fmt.Printf("tcp::error: unknown cmd\n")
			return
		}
		if rbuf[1] == STATUS_WAIT {
			fmt.Printf("tcp::dispatcher: %s\n", rbuf[2:])
			continue
		} else if rbuf[1] == STATUS_OK {
			peerAddr = string(rbuf[2:])
			break
		} else {
			fmt.Printf("tcp::dispatcher: parse peer addr failed\n")
			return
		}
	}
	fmt.Printf("tcp::dispatcher: peer found: [%s]\n", peerAddr)

	// peer
	var peerConn net.Conn
	ctx1, cancelDial := context.WithCancel(context.Background())
	ctx2, cancelListen := context.WithCancel(context.Background())
	go func() {
		for i := 0; i < 10; i++ {
			select {
			case <-ctx1.Done():
				return
			default:
				fmt.Printf("tcp::self::punch hole: direction <<\n")
				ctx, _ := context.WithTimeout(context.Background(), 2*time.Second)
				peerConn, err = dialer.DialContext(ctx, "tcp", peerAddr)
				if err == nil {
					cancelListen()
					return
				}
			}
		}
	}()
	go func() {
		for {
			select {
			case <-ctx2.Done():
				return
			default:
				peerConn, err = lis.Accept()
				if err == nil {
					cancelDial()
					return
				}
			}
		}
	}()
	if peerConn == nil {
		fmt.Printf("tcp::self::punch hole: failed\n")
		return
	}
	fmt.Printf("tcp::peer: connected! direction: << >>")

}

func serveUDP() {
	c := &net.ListenConfig{
		Control: CONTROL,
	}
	ctx := context.Background()
	conn, err := c.ListenPacket(ctx, "udp", *LADDR)
	if err != nil {
		fmt.Printf("udp::error: listen %s\n", *LADDR)
	}
	fmt.Printf("udp: listen %s\n", *LADDR)
	uconn, ok := conn.(*net.UDPConn)
	if !ok {
		fmt.Printf("udp::error: assert udp conn\n")
	}

	// send to dispatcher
	b := []byte{DISPATCHER, REGISTER_UDP, 0x20}
	hostPort := strings.SplitN(*RADDR, ":", 2)
	ip := net.ParseIP(hostPort[0])
	port, _ := strconv.Atoi(hostPort[1])
	dispatcherAddr := &net.UDPAddr{IP: ip, Port: port}
	for i := 0; i < 3; i++ {
		uconn.WriteToUDP(b, dispatcherAddr)
	}
	// recv from dispatcher or peer
	buf := make([]byte, 1024)
	for {
		n, raddr, err := uconn.ReadFromUDP(buf)
		if err != nil {
			continue
		}
		fmt.Printf("udp: [%s] -> [%s] ::\n", raddr, uconn.LocalAddr().String())

		if err != nil || n < 3 {
			fmt.Printf("udp::error: packet size = %d\n", n)
			continue
		}

		id := buf[0]
		stat := buf[1]
		if (id != DISPATCHER && id != PEER) || (stat != STATUS_WAIT && stat != STATUS_OK) {
			fmt.Printf("udp::error: unknown cmd\n")
			continue
		}
		buff := make([]byte, n)
		copy(buff, buf[:n])
		go handleUDPConn(raddr, uconn, buff)
	}
}

func handleUDPConn(raddr *net.UDPAddr, conn *net.UDPConn, data []byte) {
	id := data[0]
	stat := data[1]

	if id == DISPATCHER && stat == STATUS_WAIT {
		fmt.Printf("udp::dispatcher: %s\n", data[2:])
	} else if id == DISPATCHER && stat == STATUS_OK {
		fmt.Printf("udp::dispatcher: peer found [%s]\n", data[2:])
		peerHostPort := strings.SplitN(string(data[2:]), ":", 2)
		ip := net.ParseIP(peerHostPort[0])
		port, err := strconv.Atoi(peerHostPort[1])
		if err != nil {
			fmt.Printf("udp::self::punch hole: parse address failed, abort\n")
			return
		}
		PeerUDP = &net.UDPAddr{IP: ip, Port: port}
		punch := []byte{PEER, STATUS_WAIT, DIRECTION_FROM}
		punch2 := []byte{PEER, STATUS_WAIT, DIRECTION_TO}
		go func() {
			for i:=0;i<20;i++{
				if !PunchFrom{
					conn.WriteToUDP(punch, PeerUDP)
					fmt.Printf("udp::self::punch hole: direction <<\n")
				} else{
					conn.WriteToUDP(punch2, PeerUDP)
					fmt.Printf("udp::self::punch hole: direction >>\n")
				}
				time.Sleep(500 * time.Millisecond)
			}
		}()
	} else if id == PEER && stat == STATUS_WAIT {
		if !PunchFrom {
			PunchFrom = true
			fmt.Printf("udp::peer: connected! direction: <<\n")
		} else if !PunchTo {
			if data[2] == DIRECTION_FROM {
				punch := []byte{PEER, STATUS_WAIT, DIRECTION_TO}
				conn.WriteToUDP(punch, PeerUDP)
				fmt.Printf("udp::self::punch hole: direction >>\n")
			} else if data[2] == DIRECTION_TO {
				PunchTo = true
				fmt.Printf("udp::peer: connected! direction: >>\n")
				fmt.Printf("udp: direction: >> : %t direction: << : %t\n", PunchTo, PunchFrom)
				go func() {
					for {
						if PunchTo {
							d := []byte{PEER, STATUS_OK}
							d = append(d, []byte("keep-alive")...)
							conn.WriteToUDP(d,PeerUDP)
						} else {
							return
						}
						time.Sleep(4 * time.Second)
					}
				}()
			}
		} else if PunchTo {
			fmt.Printf("udp::peer: connected, direction: << >>\n")
		}
	} else if id == PEER && stat == STATUS_OK {
		fmt.Printf("udp::peer: data: %s\n", data[2:])
	} else {
		fmt.Printf("udp: internal error\n")
	}
}
