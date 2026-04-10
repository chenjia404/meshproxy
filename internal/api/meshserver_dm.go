package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/websocket"

	sessionv1 "github.com/chenjia404/meshproxy/internal/meshserver/sessionv1"
)

func meshResolveConnection(ms MeshServerProvider, q string) (string, error) {
	name := strings.TrimSpace(q)
	if name != "" {
		return name, nil
	}
	list := ms.ListConnections()
	if len(list) == 1 {
		return list[0].Name, nil
	}
	return "", fmt.Errorf("connection query parameter is required when multiple meshserver connections exist")
}

// handleMeshServerDM 處理 /api/v1/meshserver/dm/* 。
func (a *LocalAPI) handleMeshServerDM(w http.ResponseWriter, r *http.Request) {
	if a.opts == nil || a.opts.MeshServer == nil {
		http.Error(w, "meshserver client not available", http.StatusNotFound)
		return
	}
	ms := a.opts.MeshServer
	path := strings.TrimPrefix(r.URL.Path, "/api/v1/meshserver/dm/")
	path = strings.Trim(path, "/")
	parts := strings.Split(path, "/")

	if len(parts) == 1 && parts[0] == "ws" {
		a.handleMeshServerDMWebSocket(w, r)
		return
	}

	connection, err := meshResolveConnection(ms, r.URL.Query().Get("connection"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	switch {
	case len(parts) == 1 && parts[0] == "conversations":
		switch r.Method {
		case http.MethodGet:
			local, err := ms.DirectChatListLocal(connection)
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			if srv, err := ms.DirectChatListServer(r.Context(), connection); err == nil && srv != nil {
				_ = ms.DirectChatMergeServerList(r.Context(), connection, srv)
				local, _ = ms.DirectChatListLocal(connection)
			}
			writeJSON(w, map[string]any{"conversations": local})
		case http.MethodPost:
			var body struct {
				PeerUserID string `json:"peer_user_id"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			resp, err := ms.DirectChatOpen(r.Context(), connection, strings.TrimSpace(body.PeerUserID))
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			writeJSON(w, resp)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
		return

	case len(parts) >= 3 && parts[0] == "conversations" && parts[2] == "messages" && len(parts) == 3:
		cid := parts[1]
		switch r.Method {
		case http.MethodGet:
			limit := 100
			if v := r.URL.Query().Get("limit"); v != "" {
				if n, err := strconv.Atoi(v); err == nil {
					limit = n
				}
			}
			msgs, err := ms.DirectChatListMessagesLocal(connection, cid, limit)
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			writeJSON(w, map[string]any{"messages": msgs})
		case http.MethodPost:
			var body struct {
				ToUserID    string `json:"to_user_id"`
				ClientMsgID string `json:"client_msg_id"`
				Text        string `json:"text"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			req := &sessionv1.SendDirectMessageReq{
				ConversationId: cid,
				ToUserId:       strings.TrimSpace(body.ToUserID),
				ClientMsgId:    strings.TrimSpace(body.ClientMsgID),
				MessageType:    sessionv1.MessageType_TEXT,
				Text:           strings.TrimSpace(body.Text),
			}
			if req.ClientMsgId == "" {
				http.Error(w, "client_msg_id required", http.StatusBadRequest)
				return
			}
			ack, err := ms.DirectChatSend(r.Context(), connection, req)
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			peer := strings.TrimSpace(body.ToUserID)
			_ = ms.DirectChatApplySendAck(r.Context(), connection, ms.DirectChatLocalUserID(connection), peer, ack, req.Text)
			writeJSON(w, ack)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
		return

	case len(parts) >= 3 && parts[0] == "conversations" && parts[2] == "sync":
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		cid := parts[1]
		after := uint64(0)
		if v := r.URL.Query().Get("after_seq"); v != "" {
			if n, err := strconv.ParseUint(v, 10, 64); err == nil {
				after = n
			}
		}
		limit := uint32(50)
		if v := r.URL.Query().Get("limit"); v != "" {
			if n, err := strconv.ParseUint(v, 10, 32); err == nil {
				limit = uint32(n)
			}
		}
		sync, err := ms.DirectChatSync(r.Context(), connection, &sessionv1.SyncDirectMessagesReq{
			ConversationId: cid,
			AfterSeq:       after,
			Limit:          limit,
		})
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		_ = ms.DirectChatApplySyncMessages(r.Context(), connection, sync, ms.DirectChatLocalUserID(connection))
		writeJSON(w, sync)
		return

	case len(parts) == 3 && parts[0] == "messages" && parts[2] == "ack":
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		mid := parts[1]
		resp, err := ms.DirectChatAck(r.Context(), connection, mid)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(w, resp)
		return
	}

	http.NotFound(w, r)
}

func (a *LocalAPI) handleMeshServerDMWebSocket(w http.ResponseWriter, r *http.Request) {
	type dmSubber interface {
		SubscribeDMWebSocket() (<-chan map[string]any, func())
	}
	sub, ok := a.statusProvider.(dmSubber)
	if !ok {
		http.Error(w, "dm websocket not available", http.StatusNotFound)
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	upgrader := websocket.Upgrader{
		ReadBufferSize:  1024,
		WriteBufferSize: 1024,
		CheckOrigin:   func(r *http.Request) bool { return true },
	}
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()

	eventsCh, unsubscribe := sub.SubscribeDMWebSocket()
	defer unsubscribe()

	_ = conn.SetReadDeadline(time.Now().Add(60 * time.Second))
	conn.SetPongHandler(func(string) error {
		return conn.SetReadDeadline(time.Now().Add(60 * time.Second))
	})
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}()

	for {
		select {
		case evt, ok := <-eventsCh:
			if !ok {
				return
			}
			_ = conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := conn.WriteJSON(evt); err != nil {
				return
			}
		case <-done:
			return
		case <-r.Context().Done():
			return
		}
	}
}
