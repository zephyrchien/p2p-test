package main

import (
	"context"
	"encoding/binary"
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
	EchoOff        bool
	ActiveClient   bool
	PunchFrom      bool
	PunchTo        bool
	Network        = flag.String("net", "udp", "")
	LADDR          = flag.String("l", ":8388", "listen addr")
	RADDR          = flag.String("s", "", "remote addr")
)

// color

const (
	Black   = "\033[1;30m%s\033[0m"
	Red     = "\033[1;31m%s\033[0m"
	Green   = "\033[1;32m%s\033[0m"
	Yellow  = "\033[1;33m%s\033[0m"
	Blue    = "\033[1;34m%s\033[0m"
	Magenta = "\033[1;35m%s\033[0m"
	Teal    = "\033[1;36m%s\033[0m"
	White   = "\033[1;37m%s\033[0m"
)

func Color(color string) func(string) {
	return func(s string) {
		fmt.Printf(color, s)
	}
}

var (
	Color_Black   = Color(Black)
	Color_Red     = Color(Red)
	Color_Green   = Color(Green)
	Color_Yellow  = Color(Yellow)
	Color_Blue    = Color(Blue)
	Color_Magenta = Color(Magenta)
	Color_Teal    = Color(Teal)
	Color_White   = Color(White)
)

const BUFFERSIZE = 1024
const KEEP_ALIVE_INTERVAL = 5

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
const PING_DATA = 0x01
const PING_PAUSE = 0x02
const PING_SUMMARY = 0xff

const BENCH_START = 0x00
const BENCH_DATA = 0x01
const BENCH_PAUSE = 0x02
const BENCH_SUMMARY = 0xff

type pinger struct {
	active  bool
	lock    sync.Mutex
	max     int64
	min     int64
	sum     int64
	latency []int64
}

func (p *pinger) start() {
	p.active = true
}

func (p *pinger) pause() {
	p.active = false
}

func (p *pinger) summary() string {
	var result string
	if p.active {
		avg := int(p.sum) / len(p.latency)
		result = fmt.Sprintf("min/avg/max = %d/%d/%dms", p.min, avg, p.max)
		p.active = false
	}
	return result
}

func (p *pinger) collect(data []byte) {
	p.lock.Lock()
	defer p.lock.Unlock()
	now := time.Now()
	last := time.Time{}
	if err := last.UnmarshalBinary(data); err != nil {
		Color_Red(fmt.Sprintf("udp::self::ping: unserialize time failed, %s\n", err))
		return
	}
	d := now.Sub(last).Milliseconds()

	Color_Blue(fmt.Sprintf("udp::peer::ping::latency: %dms\n", d))
	p.latency = append(p.latency, d)
	p.sum += d
	if len(p.latency) == 1 {
		p.min = d
		p.max = d
		return
	}
	if d < p.min {
		p.min = d
	}
	if d > p.max {
		p.max = d
	}
}

type downloader struct {
	active bool
	lock   sync.Mutex
	total  uint64
}

func (dl *downloader) start() {
	dl.active = true
}

func (dl *downloader) pause() {
	dl.active = false
}

func (dl *downloader) summary() uint64 {
	if dl.active {
		dl.active = false
		return dl.total
	}
	return 0
}

func (dl *downloader) collect(n int) {
	dl.lock.Lock()
	defer dl.lock.Unlock()
	dl.total += uint64(n)
}

type limiter struct {
	lock sync.Mutex
	bs   int
	max  int
	next time.Time
}

func (lmt *limiter) set(max int) {
	lmt.bs = max
	lmt.max = max
	lmt.next = time.Now().Add(1 * time.Second)
}

func (lmt *limiter) reset() {
	lmt.bs = lmt.max
	lmt.next = time.Now().Add(1 * time.Second)
}

func (lmt *limiter) collect(n int) {
	//lmt.lock.Lock()
	//defer lmt.lock.Unlock()
	if lmt.bs < 0 {
		now := time.Now()
		delta := lmt.next.Sub(now)
		time.Sleep(delta)
	} else {
		lmt.bs -= n
	}
}

var (
	Pinger     = &pinger{latency: make([]int64, 0, 50)}
	Downloader = &downloader{}
)

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
		KeepAlive: 5 * time.Second,
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
			Color_Magenta(fmt.Sprintf("tcp::dispatcher: %s\n", rbuf[2:n]))
			continue
		} else if rbuf[1] == STATUS_OK {
			peerAddr = string(rbuf[2:n])
			break
		} else {
			Color_Red("tcp::dispatcher: parse peer addr failed\n")
			return
		}
	}
	Color_Magenta(fmt.Sprintf("tcp::dispatcher: peer found: [%s]\n", peerAddr))

	// peer
	var peerConn net.Conn
	ctx1, cancelDial := context.WithCancel(context.Background())
	ctx2, cancelListen := context.WithCancel(context.Background())
	wg := sync.WaitGroup{}
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
	go func() {
		for {
			n, err := conn.Read(rbuf)
			if err != nil {
				Color_Red(fmt.Sprintf("tcp::connection::error: %s\n", err))
				return
			}
			Color_Blue(fmt.Sprintf("tcp::peer: %s\n", rbuf[:n]))
		}
	}()

	for {
		wbuf = []byte("ciallo")
		_, err = conn.Write(wbuf)
		if err != nil {
			Color_Red(fmt.Sprintf("tcp::connection::error: %s\n", err))
			return
		}
		time.Sleep(2 * time.Second)
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
		if !EchoOff {
			Color_Yellow(fmt.Sprintf("udp: [%s] -> [%s] ::\n", raddr, uconn.LocalAddr().String()))
		}

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
	extra := data[2]
	if id == DISPATCHER && stat == STATUS_WAIT {
		if extra == 0x02 {
			ActiveClient = true
		}
		Color_Magenta(fmt.Sprintf("udp::dispatcher: %s\n", data[3:]))
	} else if id == DISPATCHER && stat == STATUS_OK {
		if extra == 0x02 {
			ActiveClient = true
		}
		Color_Magenta(fmt.Sprintf("udp::dispatcher: peer found [%s]\n", data[2:]))
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
							msg := fmt.Sprintf("keep-alive[%d]", KeepAliveCount)
							b := append([]byte{PEER, STATUS_OK, NONE}, []byte(msg)...)
							conn.WriteToUDP(b, raddr)
						} else {
							return
						}
						KeepAliveCount++
						time.Sleep(KEEP_ALIVE_INTERVAL * time.Second)
					}
				}()
			}
		} else if PunchTo {
			Color_Blue("udp::peer: connected, direction: << >>\n")
		}
	} else if id == PEER && stat == STATUS_OK {
		if extra == NONE {
			if !EchoOff {
				Color_Blue(fmt.Sprintf("udp::peer::data: %s\n", data[3:]))
			}
			triggeredUDP(raddr, conn)
		} else if extra == PING {
			switch cmd := data[3]; cmd {
			case PING_START:
				Pinger.start()
			case PING_DATA:
				Pinger.collect(data[4:])
			case PING_PAUSE:
				if result := Pinger.summary(); result != "" {
					Color_Magenta(fmt.Sprintf("udp::peer::ping::summary: %s\n", result))
					b := append([]byte{PEER, STATUS_OK, PING, PING_SUMMARY}, result...)
					conn.WriteToUDP(b, raddr)
				}
			case PING_SUMMARY:
				Color_Magenta(fmt.Sprintf("udp::peer::ping::summary %s\n", data[4:]))
			}

		} else if extra == BENCH {
			switch cmd := data[3]; cmd {
			case BENCH_START:
				Downloader.start()
			case BENCH_DATA:
				Downloader.collect(len(data))
			case BENCH_PAUSE:
				if total := Downloader.summary(); total != 0 {
					Color_Magenta(fmt.Sprintf("udp::self::download::summary: [%d kb] [%d kbs]\n", total, total/5120))
					intb := make([]byte, 8)
					binary.BigEndian.PutUint64(intb, total)
					b := append([]byte{PEER, STATUS_OK, BENCH, BENCH_SUMMARY}, intb...)
					conn.WriteToUDP(b, raddr)
				}
			case BENCH_SUMMARY:
				total := binary.BigEndian.Uint64(data[4:])
				Color_Magenta(fmt.Sprintf("udp::peer::download::summary: [%d kb] [%d kbs]\n", total, total/5120))
			}
		}
	} else {
		Color_Red("udp: unknown cmd\n")
	}
}

func triggeredUDP(raddr *net.UDPAddr, conn *net.UDPConn) {
	if KeepAliveCount < 5 {
		return
	}
	if KeepAliveCount < 24 {
		EchoOff = true
	} else {
		EchoOff = false
	}
	if KeepAliveCount < 9 && ActiveClient ||
		(KeepAliveCount >= 10 && KeepAliveCount < 13 && !ActiveClient) {
		// ping
		start := []byte{PEER, STATUS_OK, PING, PING_START}
		conn.WriteToUDP(start, raddr)
		conn.WriteToUDP(start, raddr)
		Color_Green("udp::self::ping: start\n")
		for i := 0; i < 10; i++ {
			now, err := time.Now().MarshalBinary()
			if err != nil {
				Color_Red(fmt.Sprintf("udp::self::ping::error: serialize time failed, %s\n", err))
				return
			}
			buf := append([]byte{PEER, STATUS_OK, PING, PING_DATA}, now...)
			conn.WriteToUDP(buf, raddr)
			time.Sleep(300 * time.Millisecond)
			Color_Green(fmt.Sprintf("udp::self::ping: send[%d]\n", i))
		}
		stop := []byte{PEER, STATUS_OK, PING, PING_PAUSE}
		conn.WriteToUDP(stop, raddr)
		conn.WriteToUDP(stop, raddr)
		Color_Green("udp::self::ping: pause\n")
		return
	}
	if KeepAliveCount >= 15 && KeepAliveCount < 18 && ActiveClient ||
		(KeepAliveCount >= 20 && KeepAliveCount < 23 && !ActiveClient) {
		start := []byte{PEER, STATUS_OK, BENCH, BENCH_START}
		conn.WriteToUDP(start, raddr)
		conn.WriteToUDP(start, raddr)
		Color_Green("udp::self::upload: start\n")

		var sec, bsec int
		var btotal uint64
		b := make([]byte, BUFFERSIZE)
		b[0], b[1], b[2], b[3] = PEER, STATUS_OK, BENCH, BENCH_DATA
		ticker := time.NewTicker(1 * time.Second)
		lmt := new(limiter)
		lmt.set(10 << 20)
		for sec = 0; sec < 5; {
			select {
			case <-ticker.C:
				sec++
				bs := bsec / 1024
				btotal += uint64(bsec)
				lmt.reset()
				bsec = 0
				Color_Green(fmt.Sprintf("udp::self::upload: [%d] [%d kbs]\n", sec, bs))
			default:
				n, _ := conn.WriteToUDP(b, raddr)
				bsec += n
				lmt.collect(n)
			}
		}
		Color_Magenta(fmt.Sprintf("udp::self::upload: [sum] [%d kb] [%d kbs]\n", btotal/1024, btotal/5120))
		stop := []byte{PEER, STATUS_OK, BENCH, BENCH_PAUSE}
		conn.WriteToUDP(stop, raddr)
		conn.WriteToUDP(stop, raddr)
		Color_Green("udp::self::upload: pause\n")
	}
}
