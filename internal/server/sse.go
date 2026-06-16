// Package server: реєстр SSE-підписників із неблокуючим broadcast.
package server

import (
	"sync"

	"github.com/karpenkodima/ntp-dashboard/internal/sampler"
)

// SSEBus — реєстр каналів для розсилки нових NTP-семплів через SSE.
type SSEBus struct {
	mu   sync.RWMutex
	subs map[int]chan<- sampler.Sample
	next int
}

// NewSSEBus створює порожній реєстр.
func NewSSEBus() *SSEBus { return &SSEBus{subs: map[int]chan<- sampler.Sample{}} }

// Subscribe реєструє новий канал (буферизований) і повертає id підписки та канал.
func (b *SSEBus) Subscribe(buf int) (int, chan<- sampler.Sample) {
	ch := make(chan sampler.Sample, buf)
	id := b.add(ch)
	return id, ch
}

func (b *SSEBus) add(ch chan<- sampler.Sample) int {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.next++
	id := b.next
	b.subs[id] = ch
	return id
}

// Unsubscribe видаляє підписника за id.
func (b *SSEBus) Unsubscribe(id int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if ch, ok := b.subs[id]; ok {
		delete(b.subs, id)
		_ = ch // непотрібний, але показуємо, що закриваємо логічно; GC зробить решту
	}
}

// Publish розсилає семпл усім підписникам без блокування (як Python queue.put_nowait).
func (b *SSEBus) Publish(s sampler.Sample) {
	b.mu.RLock()
	subs := make([]chan<- sampler.Sample, 0, len(b.subs))
	for _, ch := range b.subs {
		subs = append(subs, ch)
	}
	b.mu.RUnlock()
	for _, ch := range subs {
		select {
		case ch <- s:
		default:
			// відповідає except queue.Full: pass
		}
	}
}
