package eventhub

import (
	"sync"

	"github.com/quanyejun/quanyejun/runtime/internal/domain"
)

type Hub struct {
	mu          sync.Mutex
	subscribers map[chan domain.Event]struct{}
}

func New() *Hub { return &Hub{subscribers: make(map[chan domain.Event]struct{})} }

func (h *Hub) Subscribe() (<-chan domain.Event, func()) {
	channel := make(chan domain.Event, 32)
	h.mu.Lock()
	h.subscribers[channel] = struct{}{}
	h.mu.Unlock()
	return channel, func() {
		h.mu.Lock()
		if _, ok := h.subscribers[channel]; ok {
			delete(h.subscribers, channel)
			close(channel)
		}
		h.mu.Unlock()
	}
}

func (h *Hub) Publish(event domain.Event) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for subscriber := range h.subscribers {
		select {
		case subscriber <- event:
		default:
		}
	}
}
