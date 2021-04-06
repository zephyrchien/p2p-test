package main

import (
	"context"
	"flag"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"
)

var (
	KeepAliveCount int
	ActiveClient bool
	PunchFrom bool
	PunchTo   bool 
	Network   = flag.String("net", "udp", "")
	LADDR     = flag.String("l", ":8388", "listen addr")
	RADDR     = flag.String("s", "", "remote addr")
)

// color

const (
	Black   = "\033[1;30m%s\033[0m"
	Red     = "\033[1;31m%s\033[0m"
	Green   = "\033[1;32m%s\033[0m"
	Yellow  = "\033[1;33m%s\033[0m"
	Blue    = "\033[1;34m%s\033[0m"
	Purple  = "\033[1;34m%s\033[0m"
	Magenta = "\033[1;35m%s\033[0m"
	Teal    = "\033[1;36m%s\033[0m"
	White   = "\033[1;37m%s\033[0m"
)

func Color(color string)func(string){
	return func(s string){
		fmt.Printf(color,s)
	}
}

var (
	Color_Black = Color(Black)
	Color_Red = Color(Red)
	Color_Green = Color(Green)
	Color_Yellow = Color(Yellow)
	Color_Blue = Color(Blue)
	Color_Purple = Color(Purple)
	Color_Magenta = Color(Magenta)
	Color_Teal = Color(Teal)
	Color_White = Color(White)
)

const BUFFERSIZE = 1024

const DISPATCHER = 0x00
const PEER = 0x01

const REGISTER_TCP = 0x01
const REGISTER_UDP = 0x02
const STATUS_WAIT = 0x01
const STATUS_OK = 0x02

const CLIENT_FIRST = 0x01
const CLIENT_SECOND = 0x02
const DIRECTION_FROM = 0x01
const DIRECTION_TO = 0x02
const NONE = 0x00

const PING = 0x01
const BENCH = 0x02

const PING_START = 0x00
const PING_PAUSE = 0xff
const PING_CONTINUE = 0x01

type pinger struct{
	active bool
	lock sync.Mutex
	max int64
	min int64
	sum int64
	latency []int64
}

func (p *pinger)analysis(data []byte){
	switch mark:=data[0];mark{
	case 0x00:
		p.active = true
		return
	case 0xff:
		if p.active{
			avg:= int(p.sum)/len(p.latency)
			Color_Blue(fmt.Sprintf("udp::peer::ping: %d/%d/%d\n",p.min,avg,p.max))
			p.active = false
		}
	default:
		p.lock.Lock()
		defer p.lock.Unlock()
		now:=time.Now()
		last:=time.Time{}
		if err:=last.UnmarshalBinary(data[1:]);err!=nil{
			Color_Red(fmt.Sprintf("udp::self::ping: unserialize time failed, %s\n",err))
			return
		}
		d:=now.Sub(last).Milliseconds()
		Color_Blue(fmt.Sprintf("udp::peer::ping::latency: %dms\n",d))
		p.latency = append(p.latency,d)
		p.sum += d
		if len(p.latency)==1{
			p.min = d
			p.max = d
			return
		}
		if d < p.min{
			p.min = d
		}
		if d > p.max {
			p.max = d
		}
	}

}

var Pinger = &pinger{latency: make([]int64,0,50)}

func init() {
	flag.Parse()
}

func main() {
	if *RADDR == "" {
		Color_Red("no remote address specified, quit\n")
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
		Color_Red(fmt.Sprintf("tcp::error: listen %s\n", *LADDR))
	}
	defer lis.Close()
	Color_Green(fmt.Sprintf("tcp: listen %s\n", *LADDR))

	// dispatcher
	port, _ := strconv.Atoi(strings.Replace(*LADDR, ":", "", 1))
	tcpAddr := &net.TCPAddr{Port: port}
	dialer := net.Dialer{
		LocalAddr: tcpAddr,
		Control:   CONTROL,
		KeepAlive: 5*time.Second,
	}
	conn, err := dialer.Dial("tcp", *RADDR)
	if err != nil {
		Color_Red(fmt.Sprintf("tcp::error: %s\n", err))
		return
	}
	defer conn.Close()
	wbuf := []byte{DISPATCHER, REGISTER_TCP, NONE}
	if _, err := conn.Write(wbuf); err != nil {
		Color_Red(fmt.Sprintf("tcp::error: %s\n", err))
		return
	}
	rbuf := make([]byte, BUFFERSIZE)
	var peerAddr string
	for {
		n, err := conn.Read(rbuf)
		if err != nil {
			Color_Red(fmt.Sprintf("tcp::error: %s\n", err))
			return
		}
		if n < 3 || rbuf[0] != DISPATCHER {
			Color_Red("tcp::error: unknown cmd\n")
			return
		}
		if rbuf[1] == STATUS_WAIT {
			Color_Purple(fmt.Sprintf("tcp::dispatcher: %s\n", rbuf[2:n]))
			continue
		} else if rbuf[1] == STATUS_OK {
			peerAddr = string(rbuf[2:n])
			break
		} else {
			Color_Red("tcp::dispatcher: parse peer addr failed\n")
			return
		}
	}
	Color_Purple(fmt.Sprintf("tcp::dispatcher: peer found: [%s]\n", peerAddr))

	// peer
	var peerConn net.Conn
	ctx1, cancelDial := context.WithCancel(context.Background())
	ctx2, cancelListen := context.WithCancel(context.Background())
	wg:=sync.WaitGroup{}
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < 10; i++ {
			select {
			case <-ctx1.Done():
				Color_Yellow("tcp::self::connect canceled\n")
				return
			default:
				Color_Green("tcp::self::punch hole: direction <<\n")
				ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
				defer cancel()
				peerConn, err = dialer.DialContext(ctx, "tcp", peerAddr)
				if err == nil {
					cancelListen()
					Color_Green("tcp::self::connect succeed!\n")
					return
				}
			}
		}
	}()
	go func() {
		defer wg.Done()
		for {
			select {
			case <-ctx2.Done():
				Color_Yellow("tcp::self::accept canceled\n")
				return
			default:
				peerConn, err = lis.Accept()
				if err == nil {
					cancelDial()
					Color_Green("tcp::self::accept succeed!\n")
					return
				}
			}
		}
	}()
	wg.Wait()

	if peerConn == nil {
		Color_Red(fmt.Sprintf("tcp::self::punch hole: failed\n"))
		return
	}
	Color_Blue(fmt.Sprintf("tcp::peer: connected! direction: << >>\n"))
	go func(){
		for{
			n,err:=conn.Read(rbuf)
			if err!=nil{
				Color_Red(fmt.Sprintf("tcp::connection::error: %s\n",err))
				return
			}
			Color_Blue(fmt.Sprintf("tcp::peer: %s\n",rbuf[:n]))
		}
	}()

	for {
		wbuf=[]byte("ciallo")
		_,err=conn.Write(wbuf)
		if err!=nil{
			Color_Red(fmt.Sprintf("tcp::connection::error: %s\n",err))
			return
		}
		time.Sleep(2*time.Second)
	}
}

func serveUDP() {
	c := &net.ListenConfig{
		Control: CONTROL,
	}
	ctx := context.Background()
	conn, err := c.ListenPacket(ctx, "udp", *LADDR)
	if err != nil {
		Color_Red(fmt.Sprintf("udp::error: listen %s\n", *LADDR))
	}
	Color_Green(fmt.Sprintf("udp: listen %s\n", *LADDR))
	uconn, ok := conn.(*net.UDPConn)
	if !ok {
		Color_Red("udp::error: assert udp conn\n")
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
	
	for {
		buf := make([]byte, BUFFERSIZE)
		n, raddr, err := uconn.ReadFromUDP(buf)
		if err != nil {
			continue
		}
		Color_Yellow(fmt.Sprintf("udp: [%s] -> [%s] ::\n", raddr, uconn.LocalAddr().String()))

		if err != nil || n < 3 {
			Color_Red(fmt.Sprintf("udp::error: packet size = %d\n", n))
			continue
		}

		id := buf[0]
		stat := buf[1]
		if (id != DISPATCHER && id != PEER) || (stat != STATUS_WAIT && stat != STATUS_OK) {
			Color_Red(fmt.Sprintf("udp::error: unknown cmd\n"))
			continue
		}
		go handleUDPConn(raddr, uconn, buf[:n])
	}
}

func handleUDPConn(raddr *net.UDPAddr, conn *net.UDPConn, data []byte) {
	id := data[0]
	stat := data[1]
	extra:=data[2]
	if id == DISPATCHER && stat == STATUS_WAIT {
		if extra == 0x02 {
			ActiveClient = true
		}
		Color_Purple(fmt.Sprintf("udp::dispatcher: %s\n", data[3:]))
	} else if id == DISPATCHER && stat == STATUS_OK {
		if extra == 0x02 {
			ActiveClient = true
		}
		Color_Purple(fmt.Sprintf("udp::dispatcher: peer found [%s]\n", data[2:]))
		peerHostPort := strings.SplitN(string(data[3:]), ":", 2)
		ip := net.ParseIP(peerHostPort[0])
		port, err := strconv.Atoi(peerHostPort[1])
		if err != nil {
			Color_Red("udp::self::punch hole: parse address failed, abort\n")
			return
		}
		peer := &net.UDPAddr{IP: ip, Port: port}
		punch := []byte{PEER, STATUS_WAIT, DIRECTION_FROM}
		punch2 := []byte{PEER, STATUS_WAIT, DIRECTION_TO}
		go func() {
			for i := 0; i < 20; i++ {
				if !PunchFrom {
					conn.WriteToUDP(punch, peer)
					Color_Green("udp::self::punch hole: direction <<\n")
				} else {
					conn.WriteToUDP(punch2, peer)
					Color_Green("udp::self::punch hole: direction >>\n")
				}
				time.Sleep(500 * time.Millisecond)
			}
		}()
	} else if id == PEER && stat == STATUS_WAIT {
		if !PunchFrom {
			PunchFrom = true
			Color_Blue("udp::peer: connected! direction: <<\n")
		} else if !PunchTo {
			if extra == DIRECTION_FROM {
				punch := []byte{PEER, STATUS_WAIT, DIRECTION_TO}
				conn.WriteToUDP(punch, raddr)
				Color_Green("udp::self::punch hole: direction >>\n")
			} else if extra == DIRECTION_TO {
				PunchTo = true
				Color_Blue("udp::peer: connected! direction: >>\n")
				Color_Yellow(fmt.Sprintf("udp: direction: >> : %t direction: << : %t\n", PunchTo, PunchFrom))
				go func() {
					for {
						if PunchTo {
							msg:=fmt.Sprintf("keep-alive[%d]",KeepAliveCount)
							d := append([]byte{PEER, STATUS_OK,NONE},[]byte(msg)...)
							conn.WriteToUDP(d, raddr)
						} else {
							return
						}
						KeepAliveCount++
						time.Sleep(4 * time.Second)
					}
				}()
			}
		} else if PunchTo {
			Color_Blue("udp::peer: connected, direction: << >>\n")
		}
	} else if id == PEER && stat == STATUS_OK {
		if extra == NONE{
			Color_Blue(fmt.Sprintf("udp::peer::data: %s\n", data[3:]))
			triggeredUDP(raddr,conn)
		}else if extra == PING{
			Pinger.analysis(data[3:])
		}else if extra == BENCH{

		}
	} else {
		Color_Red("udp: unknown cmd\n")
	}
}

func triggeredUDP(raddr *net.UDPAddr, conn *net.UDPConn) {
	if KeepAliveCount < 5 {
		return
	}
	if KeepAliveCount < 10 {
		// ping
		start:=[]byte{PEER,STATUS_OK,PING,PING_START}
		conn.WriteToUDP(start,raddr)
		conn.WriteToUDP(start,raddr)
		Color_Green("udp::self::ping: start\n")
		for i:=0;i<10;i++{
			now,err:=time.Now().MarshalBinary()
			if err!=nil{
				Color_Red(fmt.Sprintf("udp::self::ping::error: serialize time failed, %s\n",err))
				return
			}
			buf:=append([]byte{PEER,STATUS_OK,PING,PING_CONTINUE},now...)
			conn.WriteToUDP(buf,raddr)
			time.Sleep(300*time.Millisecond)
			Color_Green(fmt.Sprintf("udp::self::ping: send[%d]\n",i))
		}
		stop:=[]byte{PEER,STATUS_OK,PING,PING_PAUSE}
		conn.WriteToUDP(stop,raddr)
		conn.WriteToUDP(stop,raddr)
		Color_Green("udp::self::ping: pause\n")
	}
}
