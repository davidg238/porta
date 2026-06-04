// Copyright (c) 2026 Ekorau LLC

// Package debugui serves a browser-based debug viewer.
package debugui

import "sync"

// Event is an SSE event with a named type and pre-rendered HTML body.
type Event struct {
	Name string // "debug-status", "debug-source", "debug-stack", "debug-locals"
	HTML string // pre-rendered HTML fragment
}

// Hub manages per-device SSE subscriptions.
type Hub struct {
	mu      sync.RWMutex
	clients map[string]map[chan Event]struct{} // deviceID -> set of channels
}

// NewHub creates an empty Hub.
func NewHub() *Hub {
	return &Hub{
		clients: make(map[string]map[chan Event]struct{}),
	}
}

// Subscribe returns a channel that receives events for the given device.
func (h *Hub) Subscribe(deviceID string) chan Event {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.clients[deviceID] == nil {
		h.clients[deviceID] = make(map[chan Event]struct{})
	}
	ch := make(chan Event, 16)
	h.clients[deviceID][ch] = struct{}{}
	return ch
}

// Unsubscribe removes a channel from the device's subscriber set.
func (h *Hub) Unsubscribe(deviceID string, ch chan Event) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if subs, ok := h.clients[deviceID]; ok {
		delete(subs, ch)
		if len(subs) == 0 {
			delete(h.clients, deviceID)
		}
	}
}

// Broadcast sends an event to all subscribers for a device. Non-blocking per client.
func (h *Hub) Broadcast(deviceID string, evt Event) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	for ch := range h.clients[deviceID] {
		select {
		case ch <- evt:
		default: // drop if client is slow
		}
	}
}
