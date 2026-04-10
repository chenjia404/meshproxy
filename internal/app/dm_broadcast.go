package app

// broadcastDMEvent 向所有 DM WebSocket 訂閱者廣播 JSON 事件。
func (a *App) broadcastDMEvent(ev map[string]any) {
	if a == nil {
		return
	}
	a.dmSubsMu.Lock()
	defer a.dmSubsMu.Unlock()
	if a.dmSubs == nil {
		return
	}
	for ch := range a.dmSubs {
		select {
		case ch <- ev:
		default:
		}
	}
}

// SubscribeDMWebSocket 訂閱中心化私聊事件（與 chat ws 分離）。
func (a *App) SubscribeDMWebSocket() (<-chan map[string]any, func()) {
	ch := make(chan map[string]any, 64)
	a.dmSubsMu.Lock()
	if a.dmSubs == nil {
		a.dmSubs = make(map[chan map[string]any]struct{})
	}
	a.dmSubs[ch] = struct{}{}
	a.dmSubsMu.Unlock()
	unsub := func() {
		a.dmSubsMu.Lock()
		delete(a.dmSubs, ch)
		a.dmSubsMu.Unlock()
		close(ch)
	}
	return ch, unsub
}
