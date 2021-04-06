package main

import (
	"bufio"
	"bytes"
	"context"
	"flag"
	"fmt"
	"golang.org/x/sys/unix"
	"net"
	"os"
	"syscall"
)

var (
	port    = flag.Int("p", 8388, "")
	isEcho  = flag.Bool("echo", false, "")
	network = flag.String("net", "tcp", "")
	target  string
)

var CONTROL = func(network, address string, c syscall.RawConn) error {
	return c.Control(func(fd uintptr) {
		syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, unix.SO_REUSEADDR, 1)
		syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, unix.SO_REUSEPORT, 1)
	})
}

func init() {
	flag.Parse()
	if flag.NArg() != 1 {
		fmt.Println("no remote addr specified")
		os.Exit(0)
	}
	target = flag.Args()[0]
	fmt.Println("target:", target)
	fmt.Println("network: ", *network)
	fmt.Println("local: ", *port)
	fmt.Println("echo: ", *isEcho)
}

func main() {
	go echoTCP(*port)
	go echoUDP(*port)
	ch := make(chan []byte, 10)
	go input(ch)
	laddr := &net.TCPAddr{Port: *port}
	dialer := net.Dialer{
		Control:   CONTROL,
		LocalAddr: laddr,
	}
	tcpConn, err := dialer.Dial("tcp", target)
	if err == nil {
		go recvTCP(tcpConn)
	} else {
		fmt.Println("tcp: no connection")
		//os.Exit(0)
	}
	for text := range ch {
		if *network == "udp" {
			callUDP(target, *port, text)
		} else if *network == "tcp" {
			if _, err := tcpConn.Write(text); err != nil {
				tcpConn, err := dialer.Dial("tcp", target)
				if err == nil {
					go recvTCP(tcpConn)
				} else {
					fmt.Println("tcp: no connection")
					os.Exit(0)
				}
			}
		}
	}
}

func input(ch chan []byte) {
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		text := scanner.Bytes()
		if bytes.HasPrefix(text, []byte("/switch")) {
			dst := bytes.SplitN(text, []byte{0x20}, 2)[1]
			target = string(dst)
			fmt.Printf("new target: %s\n", target)
			continue
		} else if bytes.HasPrefix(text, []byte("/net")) {
			nw := bytes.SplitN(text, []byte{0x20}, 2)[1]
			*network = string(nw)
			fmt.Printf("network: %s\n", *network)
			continue
		}
		ch <- text
	}
}

func callUDP(target string, port int, msg []byte) {
	laddr := &net.UDPAddr{Port: port}
	dialer := net.Dialer{
		Control:   CONTROL,
		LocalAddr: laddr,
	}
	conn, err := dialer.Dial("udp", target)
	if err != nil {
		fmt.Println(err)
		return
	}
	defer conn.Close()
	conn.Write(msg)
}

func recvTCP(conn net.Conn) {
	buf := make([]byte, 1024)
	for {
		raddr := conn.RemoteAddr().String()
		n, err := conn.Read(buf)
		if err != nil {
			fmt.Println(err)
			return
		}
		fmt.Printf("tcp: [%s]::\n", raddr)
		fmt.Printf("recv: %s", buf[:n])
		if *isEcho {
			buff := append(buf[:n], []byte(" << "+raddr)...)
			conn.Write(buff)
			fmt.Printf(" >>\n")
		} else {
			fmt.Printf("\n")
		}
	}
}

func echoTCP(port int) {
	addr := fmt.Sprintf(":%d", port)
	//tcpAddr := &net.TCPAddr{Port: port}
	lisc := net.ListenConfig{
		Control: CONTROL,
	}
	/*
		dialer := net.Dialer{
			LocalAddr: tcpAddr,
			Control:   CONTROL,
		}
	*/
	ctx := context.Background()
	lis, err := lisc.Listen(ctx, "tcp", addr)
	if err != nil {
		fmt.Println(err)
		return
	}
	fmt.Printf("tcp: listen %s\n", lis.Addr().String())
	for {
		conn, err := lis.Accept()
		if err != nil {
			fmt.Println(err)
			return
		}
		go recvTCP(conn)
	}
}

func echoUDP(port int) {
	addr := fmt.Sprintf(":%d", port)
	ctx := context.Background()
	lisc := net.ListenConfig{
		Control: CONTROL,
	}
	lis, err := lisc.ListenPacket(ctx, "udp", addr)
	if err != nil {
		fmt.Println(err)
	}
	fmt.Printf("udp: listen %s\n", lis.LocalAddr().String())
	conn, _ := lis.(*net.UDPConn)
	buf := make([]byte, 1024)
	for {
		n, raddr, err := conn.ReadFromUDP(buf)
		if err != nil {
			fmt.Println(err)
		}
		fmt.Printf("udp: [%s]::\n", raddr.String())
		fmt.Printf("recv: %s", buf[:n])
		if !*isEcho {
			fmt.Printf("\n")
			continue
		}
		buff := append(buf[:n], []byte(" << "+raddr.String())...)
		conn.WriteToUDP(buff, raddr)
		fmt.Printf(" >>\n")
	}
}
