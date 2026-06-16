package fetcher

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
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
}

type Endpoint struct {
	Addr          string
	Port          string
	Label         string
	UA            string
	NtpName       string
	HasCloudflare bool
}

var endpoints = []Endpoint{
	{Addr: "84.21.160.223", Port: "8000", Label: "UA ios", UA: "ios", HasCloudflare: false},
	{Addr: "84.21.161.205", Port: "8000", Label: "UA android", UA: "android", HasCloudflare: false},
	{Addr: "196.16.109.59", Port: "8000", Label: "US io", UA: "io", NtpName: "cloudflare", HasCloudflare: true},
	{Addr: "196.16.111.179", Port: "8000", Label: "US android", UA: "android", NtpName: "cloudflare", HasCloudflare: true},
}

func New(db *store.DB, interval time.Duration) *Fetcher {
	ctx, cancel := context.WithCancel(context.Background())
	return &Fetcher{
		db:       db,
		interval: interval,
		ctx:      ctx,
		cancel:   cancel,
	}
}

func (f *Fetcher) Start() {
	for _, ep := range endpoints {
		f.wg.Add(1)
		go f.loop(ep)
	}
}

func (f *Fetcher) Stop() {
	f.cancel()
	f.wg.Wait()
}

func (f *Fetcher) loop(ep Endpoint) {
	defer f.wg.Done()

	client := f.newClient()

	for {
		select {
		case <-f.ctx.Done():
			return
		default:
		}

		serverMs, _ := f.fetchTime(client, ep, "/ntp/server-time")
		cfMs := int64(0)
		if ep.HasCloudflare {
			cfMs, _ = f.fetchTime(client, ep, "/ntp/cloudflare")
		}

		now := time.Now().UTC()
		dateTime := now.Format("2006-01-02 15:04:05.000")
		unixMs := now.UnixMilli()

		if err := f.db.Insert(ep.Label, dateTime, unixMs, serverMs, cfMs, ep.NtpName); err != nil {
			log.Printf("insert %s: %v", ep.Label, err)
		}

		timer := time.NewTimer(f.interval)
		select {
		case <-f.ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

func (f *Fetcher) newClient() *http.Client {
	return &http.Client{
		Timeout: 10 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		},
	}
}

func (f *Fetcher) fetchTime(client *http.Client, ep Endpoint, path string) (int64, error) {
	url := fmt.Sprintf("https://%s:%s%s", ep.Addr, ep.Port, path)
	req, err := http.NewRequestWithContext(f.ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Accept", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1024))
	if err != nil {
		return 0, err
	}
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("status %d", resp.StatusCode)
	}

	txt := strings.TrimSpace(string(body))

	// Try JSON first
	if strings.HasPrefix(txt, "{") {
		// Extract ts field: "ts": 1234567890.123
		idx := strings.Index(txt, `"ts"`)
		if idx >= 0 {
			colon := strings.Index(txt[idx:], ":")
			if colon >= 0 {
				start := idx + colon + 1
				end := strings.IndexAny(txt[start:], ",}")
				if end < 0 {
					end = len(txt) - start
				}
				valStr := strings.TrimSpace(txt[start : start+end])
				if v, err := strconv.ParseFloat(valStr, 64); err == nil {
					return int64(v * 1000), nil
				}
			}
		}
	}

	// Try plain text integer (unix seconds or ms)
	if v, err := strconv.ParseInt(txt, 10, 64); err == nil {
		if v > 1e12 {
			return v, nil // already milliseconds
		}
		return v * 1000, nil // seconds -> ms
	}

	// Try RFC3339
	if t, err := time.Parse(time.RFC3339Nano, txt); err == nil {
		return t.UnixMilli(), nil
	}
	if t, err := time.Parse(time.RFC3339, txt); err == nil {
		return t.UnixMilli(), nil
	}

	return 0, fmt.Errorf("unparseable time: %q", txt)
}
