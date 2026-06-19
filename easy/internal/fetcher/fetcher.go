package fetcher

import (
	"context"
	"encoding/binary"
	"log"
	"net"
	"sync"
	"time"

	"ntp/easy/internal/store"
)

type Fetcher struct {
	db       *store.DB
	interval time.Duration
	ctx      context.Context
	cancel   context.CancelFunc
	wg       sync.WaitGroup
	mu       sync.RWMutex
	srvIdx   int
}

type Endpoint struct {
	Host  string
	Label string
}

var endpoints = []Endpoint{
	{Host: "time.apple.com", Label: "apple"},
	{Host: "time.google.com", Label: "google"},
	{Host: "time.nist.gov", Label: "nist"},
}

func New(db *store.DB, interval time.Duration) *Fetcher {
	if interval < 5*time.Second {
		interval = 10 * time.Second
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Fetcher{
		db:       db,
		interval: interval,
		ctx:      ctx,
		cancel:   cancel,
	}
}

func (f *Fetcher) Interval() time.Duration {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.interval
}

func (f *Fetcher) SetInterval(d time.Duration) {
	if d < 5*time.Second {
		d = 5 * time.Second
	}
	f.mu.Lock()
	f.interval = d
	f.mu.Unlock()
}

func (f *Fetcher) Start() {
	f.wg.Add(1)
	go f.loop()
}

func (f *Fetcher) Stop() {
	f.cancel()
	f.wg.Wait()
}

func (f *Fetcher) loop() {
	defer f.wg.Done()

	for {
		select {
		case <-f.ctx.Done():
			return
		default:
		}

		iv := f.Interval()
		var wg sync.WaitGroup
		for _, ep := range endpoints {
			wg.Add(1)
			go func(ep Endpoint) {
				defer wg.Done()
				serverMs, offsetMs := f.queryNTP(ep.Host)
				now := time.Now().UTC()
				dateTime := now.Format("2006-01-02 15:04:05.000")
				unixMs := now.UnixMilli()
				serverMsFromOffset := unixMs + offsetMs
				if serverMs == 0 && offsetMs != 0 {
					serverMs = serverMsFromOffset
				}
				if err := f.db.Insert(ep.Label, dateTime, unixMs, serverMs, 0, ep.Host); err != nil {
					log.Printf("insert %s: %v", ep.Label, err)
				}
			}(ep)
		}
		wg.Wait()

		timer := time.NewTimer(iv)
		select {
		case <-f.ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

// NTP epoch offset: 1900-01-01 to 1970-01-01 = 2,208,988,800 seconds
const ntpEpochOffset = 2208988800

func (f *Fetcher) queryNTP(host string) (serverMs int64, offsetMs int64) {
	addr := net.JoinHostPort(host, "123")
	conn, err := net.DialTimeout("udp", addr, 5*time.Second)
	if err != nil {
		log.Printf("ntp dial %s: %v", host, err)
		return 0, 0
	}
	defer conn.Close()

	// NTP request packet (48 bytes)
	// LI=0, VN=3, Mode=3 (client) -> 0x1B
	req := make([]byte, 48)
	req[0] = 0x1B

	t0 := time.Now().UnixNano() // t0: client transmit time

	if _, err := conn.Write(req); err != nil {
		log.Printf("ntp write %s: %v", host, err)
		return 0, 0
	}

	resp := make([]byte, 48)
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	if _, err := conn.Read(resp); err != nil {
		log.Printf("ntp read %s: %v", host, err)
		return 0, 0
	}

	t3 := time.Now().UnixNano() // t3: client receive time

	// Extract transmit timestamp (bytes 40-47)
	// TX time = server time when it sent the packet
	txSec := binary.BigEndian.Uint32(resp[40:44])
	txFrac := binary.BigEndian.Uint32(resp[44:48])

	serverSec := int64(txSec) - ntpEpochOffset
	serverFrac := float64(txFrac) / (1 << 32)
	serverUnixMs := int64((float64(serverSec) + serverFrac) * 1000)

	// Extract receive timestamp (bytes 32-39)
	rxSec := binary.BigEndian.Uint32(resp[32:36])
	rxFrac := binary.BigEndian.Uint32(resp[36:40])
	serverRxSec := int64(rxSec) - ntpEpochOffset
	serverRxFrac := float64(rxFrac) / (1 << 32)
	serverRxMs := int64((float64(serverRxSec) + serverRxFrac) * 1000)

	// Offset: ((t1 - t0) + (t2 - t3)) / 2
	// t0 = client send, t1 = server receive, t2 = server transmit, t3 = client receive
	// Simplified: offset = serverRxMs - t0/1e6 ... actually let's use transmit time as reference
	t0Ms := t0 / 1e6
	t3Ms := t3 / 1e6

	// offset = ((serverRxMs - t0Ms) + (serverUnixMs - t3Ms)) / 2
	offset := ((serverRxMs - t0Ms) + (serverUnixMs - t3Ms)) / 2

	return serverUnixMs, offset
}
