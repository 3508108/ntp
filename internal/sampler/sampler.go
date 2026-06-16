// Package sampler реалізує ядро NTP-рушія та ланцюжок випадковості (qrandom.io →
// random.org → локальний PRNG).
package sampler

import (
	"log"
	"math"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	ntp "github.com/beevik/ntp"
)

// Константи, що відповідають Python ntp_sampler.py.
const (
	HeartbeatInterval = 2 * time.Second
	DowntimeThreshold = 8 * time.Second
	NISTURL           = "https://tf.nist.gov/tf-cgi/servers.cgi"
	ntpRequestTimeout = 5 * time.Second
)

// Status — форма для /ntp/status.
type Status struct {
	Running      bool    `json:"running"`
	Total        int64   `json:"total"`
	NextIn       int     `json:"next_in"`
	ServersCount int     `json:"servers_count"`
	DbSizeKB     float64 `json:"db_size_kb"`
	Last         *Sample `json:"last"`
}

// Sample — NTP-семпл у JSON-формі (як Python dict).
type Sample struct {
	Server   string   `json:"server"`
	OffsetMs *float64 `json:"offset_ms,omitempty"`
	DelayMs  *float64 `json:"delay_ms,omitempty"`
	Stratum  *int     `json:"stratum,omitempty"`
	RandIdx  int      `json:"rand_idx"`
	RandSrc  string   `json:"rand_src"`
	NextSec  int      `json:"next_sec"`
	OK       bool     `json:"ok"`
	Error    *string  `json:"error,omitempty"`
	Ts       float64  `json:"ts"`
	TsFmt    string   `json:"ts_fmt"`
}

// StoreLike — мінімальний інтерфейс, який потрібен Sampler-у.
// Конкретний *store.Store його задовольняє напряму.
type StoreLike interface {
	InsertSample(r SampleRow) error
	InsertHeartbeat(ts time.Time) error
	PruneHeartbeats() error
	LastHeartbeat() (time.Time, bool, error)
	InsertDowntime(start, end time.Time, dur float64, reason string) error
	TotalSamples() (int64, error)
	ClearSamples() error
	InsertDeploy(deployedAt time.Time, durationMs *int, gitHash, message string) error
}

// SampleRow — рядок NTP-семпла, що передається на збереження.
type SampleRow struct {
	Server    string
	OffsetMs  *float64
	DelayMs   *float64
	Stratum   *int
	RandIdx   int
	NextSec   int
	OK        bool
	Error     string
	RandSrc   string
	Timestamp time.Time
}

// Sampler — основний рушій NTP-семплінгу.
type Sampler struct {
	store       StoreLike
	intervalMin int
	intervalMax int
	dbPath      string

	mu          sync.RWMutex
	running     bool
	nextIn      int
	servers     []string
	lastSample  *Sample
	total       int64

	subsMu      sync.RWMutex
	subscribers []chan<- Sample
}

// New створює Sampler.
func New(store StoreLike, dbPath string, intervalMin, intervalMax int) *Sampler {
	if intervalMin <= 0 {
		intervalMin = 30
	}
	if intervalMax <= intervalMin {
		intervalMax = intervalMin + 90
	}
	s := &Sampler{
		store:       store,
		dbPath:      dbPath,
		intervalMin: intervalMin,
		intervalMax: intervalMax,
	}
	if total, err := store.TotalSamples(); err == nil {
		s.total = total
	}
	s.detectStartupDowntime()
	s.refreshServers()
	return s
}

// Start запускає основний цикл семплінгу і heartbeat.
func (s *Sampler) Start() {
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return
	}
	s.running = true
	s.mu.Unlock()

	go s.runLoop()
	go s.heartbeatLoop()
}

// Stop сигналізує про зупинку.
func (s *Sampler) Stop() {
	s.mu.Lock()
	s.running = false
	s.mu.Unlock()
}

// GracefulStop завершує роботу без хибних downtime: записує фінальний heartbeat
// і дає циклам завершитися.
func (s *Sampler) GracefulStop(sampleTimeout, hbTimeout time.Duration) {
	s.mu.Lock()
	s.running = false
	s.mu.Unlock()

	_ = s.store.InsertHeartbeat(time.Now())

	time.Sleep(sampleTimeout)
}

func (s *Sampler) heartbeatLoop() {
	ticker := time.NewTicker(HeartbeatInterval)
	defer ticker.Stop()
	for {
		s.mu.RLock()
		running := s.running
		s.mu.RUnlock()
		if !running {
			return
		}
		<-ticker.C
		if err := s.store.InsertHeartbeat(time.Now()); err != nil {
			log.Printf("heartbeat insert: %v", err)
		}
		if err := s.store.PruneHeartbeats(); err != nil {
			log.Printf("heartbeat prune: %v", err)
		}
	}
}

func (s *Sampler) runLoop() {
	s.refreshServers()

	countdown, _ := RandInt(5, 15)
	for {
		s.mu.RLock()
		running := s.running
		s.mu.RUnlock()
		if !running {
			return
		}
		s.mu.Lock()
		s.nextIn = int(math.Max(0, float64(countdown)))
		s.mu.Unlock()

		time.Sleep(1 * time.Second)
		countdown--

		if countdown <= 0 {
			sample := s.doSample()
			s.publish(sample)
			countdown = sample.NextSec
		}
	}
}

func (s *Sampler) doSample() Sample {
	s.mu.RLock()
	servers := append([]string(nil), s.servers...)
	s.mu.RUnlock()

	randIdx, randSrc := RandInt(0, len(servers)-1)
	nextSec, _ := RandInt(s.intervalMin, s.intervalMax)
	server := servers[randIdx]
	now := time.Now()

	var (
		offsetMs *float64
		delayMs  *float64
		stratum  *int
		ok       = true
		errStr   string
	)

	resp, err := ntp.QueryWithOptions(server, ntp.QueryOptions{Timeout: ntpRequestTimeout})
	if err != nil {
		ok = false
		errStr = truncate(err.Error(), 120)
	} else {
		v := round3(resp.ClockOffset.Seconds() * 1000)
		offsetMs = &v
		d := round3(resp.RTT.Seconds() * 1000)
		delayMs = &d
		st := int(resp.Stratum)
		stratum = &st
	}

	srcName := string(randSrc)
	if srcName == "" {
		srcName = "local"
	}

	if err := s.store.InsertSample(SampleRow{
		Server:    server,
		OffsetMs:  offsetMs,
		DelayMs:   delayMs,
		Stratum:   stratum,
		RandIdx:   randIdx,
		NextSec:   nextSec,
		OK:        ok,
		Error:     errStr,
		RandSrc:   srcName,
		Timestamp: now,
	}); err != nil {
		log.Printf("insert sample: %v", err)
	}

	sample := Sample{
		Server:   server,
		OffsetMs: offsetMs,
		DelayMs:  delayMs,
		Stratum:  stratum,
		RandIdx:  randIdx,
		RandSrc:  srcName,
		NextSec:  nextSec,
		OK:       ok,
		Ts:       float64(now.Unix()),
		TsFmt:    now.UTC().Format("15:04:05"),
	}
	if !ok {
		e := errStr
		sample.Error = &e
	}

	s.mu.Lock()
	s.lastSample = &sample
	s.total++
	s.mu.Unlock()

	return sample
}

func (s *Sampler) detectStartupDowntime() {
	now := time.Now()
	last, ok, err := s.store.LastHeartbeat()
	if err != nil || !ok {
		return
	}
	gap := now.Sub(last)
	if gap > DowntimeThreshold {
		startedAt := last.Add(HeartbeatInterval)
		dur := round1(now.Sub(startedAt).Seconds())
		_ = s.store.InsertDowntime(startedAt, now, dur, "service_restart")
	}
}

func (s *Sampler) refreshServers() {
	var nist []string
	resp, err := httpClient.Get(NISTURL)
	if err == nil {
		text := readBodyLimited(resp.Body, 64*1024)
		resp.Body.Close()
		re := regexp.MustCompile(`\b([a-z0-9][a-z0-9\-\.]*\.nist\.gov)\b`)
		matches := re.FindAllStringSubmatch(text, -1)
		seen := map[string]bool{}
		for _, m := range matches {
			host := m[1]
			if hasAnyPrefix(host, skipPrefixes) {
				continue
			}
			if seen[host] {
				continue
			}
			seen[host] = true
			nist = append(nist, host)
		}
	}
	if len(nist) < 5 {
		nist = append([]string(nil), fallbackServers...)
	}
	merged := []string{}
	seen := map[string]bool{}
	for _, h := range append(nist, extraServers...) {
		if blockedServers[h] {
			continue
		}
		if seen[h] {
			continue
		}
		seen[h] = true
		merged = append(merged, h)
	}

	s.mu.Lock()
	s.servers = merged
	s.mu.Unlock()
}

// Subscribe реєструє канал для отримання нових семплів.
func (s *Sampler) Subscribe(ch chan<- Sample) {
	s.subsMu.Lock()
	s.subscribers = append(s.subscribers, ch)
	s.subsMu.Unlock()
}

// publish розсилає семпл усім підписникам без блокування.
func (s *Sampler) publish(sample Sample) {
	s.subsMu.RLock()
	subs := make([]chan<- Sample, len(s.subscribers))
	copy(subs, s.subscribers)
	s.subsMu.RUnlock()
	for _, ch := range subs {
		select {
		case ch <- sample:
		default:
		}
	}
}

// Status повертає поточний стан для /ntp/status.
func (s *Sampler) Status() Status {
	var size float64
	if fi, err := statFile(s.dbPath); err == nil {
		size = round1(float64(fi.Size()) / 1024.0)
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return Status{
		Running:      s.running,
		Total:        s.total,
		NextIn:       s.nextIn,
		ServersCount: len(s.servers),
		DbSizeKB:     size,
		Last:         s.lastSample,
	}
}

// Servers повертає копію списку серверів.
func (s *Sampler) Servers() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]string, len(s.servers))
	copy(out, s.servers)
	return out
}

// ClearSamples очищає ntp_samples і скидає lastSample/total.
func (s *Sampler) ClearSamples() error {
	if err := s.store.ClearSamples(); err != nil {
		return err
	}
	s.mu.Lock()
	s.lastSample = nil
	s.total = 0
	s.mu.Unlock()
	return nil
}

// LogDeploy записує deploy-подію; обрізає hash до 12 символів і message до 120.
func (s *Sampler) LogDeploy(durationMs *int, hash, message string) error {
	if len(hash) > 12 {
		hash = hash[:12]
	}
	if len(message) > 120 {
		message = message[:120]
	}
	return s.store.InsertDeploy(time.Now(), durationMs, hash, message)
}

// helpers

var httpClient = &http.Client{Timeout: 5 * time.Second}

var extraServers = []string{
	"time.cloudflare.com",
	"time.google.com",
	"time1.google.com",
	"time2.google.com",
	"time3.google.com",
	"time4.google.com",
	"0.pool.ntp.org",
	"1.pool.ntp.org",
	"2.pool.ntp.org",
	"3.pool.ntp.org",
}

var blockedServers = map[string]bool{
	"ntp-b.nist.gov":   true,
	"ntp-d.nist.gov":   true,
	"ntp-wwv.nist.gov": true,
}

var skipPrefixes = []string{"www.", "ftp.", "mail.", "smtp."}

var fallbackServers = []string{
	"time-a-g.nist.gov", "time-b-g.nist.gov", "time-c-g.nist.gov",
	"time-d-g.nist.gov", "time-e-g.nist.gov", "time-f-g.nist.gov",
	"time-a-wwv.nist.gov", "time-b-wwv.nist.gov", "time-c-wwv.nist.gov",
	"time-d-wwv.nist.gov", "time-e-wwv.nist.gov",
	"time-a-b.nist.gov", "time-b-b.nist.gov", "time-c-b.nist.gov",
	"time-d-b.nist.gov", "time-e-b.nist.gov",
	"ntp.nist.gov",
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

func round1(x float64) float64 {
	if x >= 0 {
		return float64(int(x*10+0.5)) / 10
	}
	return float64(int(x*10-0.5)) / 10
}

func round3(x float64) float64 {
	if x >= 0 {
		return float64(int(x*1000+0.5)) / 1000
	}
	return float64(int(x*1000-0.5)) / 1000
}

func hasAnyPrefix(s string, prefixes []string) bool {
	for _, p := range prefixes {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	return false
}
