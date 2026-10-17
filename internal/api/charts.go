package api

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"

	marketcharts "scalp-assist/internal/charts"
	"scalp-assist/internal/store"
)

type chartCommand struct {
	Type         string `json:"type"`
	SlotID       string `json:"slot_id"`
	InstrumentID string `json:"instrument_id"`
	Interval     string `json:"interval"`
}

type chartSubscription struct {
	cancel      context.CancelFunc
	unsubscribe func()
}

func (s *Server) chartInstruments(w http.ResponseWriter, r *http.Request, _ store.User) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	if s.charts == nil {
		writeError(w, http.StatusServiceUnavailable, "charts_unavailable", "chart service is unavailable")
		return
	}
	if len(s.charts.Instruments()) == 0 {
		refreshCtx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
		err := s.charts.Refresh(refreshCtx)
		cancel()
		if err != nil {
			writeError(w, http.StatusBadGateway, "chart_catalog_unavailable", err.Error())
			return
		}
	}
	writeJSON(w, http.StatusOK, s.charts.Instruments())
}

func (s *Server) chartStream(w http.ResponseWriter, r *http.Request, _ store.User) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	if s.charts == nil {
		writeError(w, http.StatusServiceUnavailable, "charts_unavailable", "chart service is unavailable")
		return
	}
	conn, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close(websocket.StatusNormalClosure, "")
	conn.SetReadLimit(4096)

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	outgoing := make(chan any, 256)
	writerDone := make(chan struct{})
	go chartSocketWriter(ctx, conn, outgoing, writerDone)

	subscriptions := make(map[string]chartSubscription)
	defer func() {
		for _, subscription := range subscriptions {
			subscription.cancel()
			subscription.unsubscribe()
		}
		cancel()
		<-writerDone
	}()

	send := func(value any) bool {
		select {
		case outgoing <- value:
			return true
		case <-ctx.Done():
			return false
		}
	}
	sendError := func(slotID, message string) {
		send(map[string]any{"type": "error", "slot_id": slotID, "error": message})
	}

	for {
		var command chartCommand
		if err := wsjson.Read(ctx, conn, &command); err != nil {
			return
		}
		command.Type = strings.ToLower(strings.TrimSpace(command.Type))
		command.SlotID = strings.TrimSpace(command.SlotID)
		if command.SlotID == "" {
			sendError("", "slot_id is required")
			continue
		}
		if command.Type == "unsubscribe" {
			if subscription, ok := subscriptions[command.SlotID]; ok {
				subscription.cancel()
				subscription.unsubscribe()
				delete(subscriptions, command.SlotID)
			}
			continue
		}
		if command.Type != "subscribe" {
			sendError(command.SlotID, "command type must be subscribe or unsubscribe")
			continue
		}
		if _, exists := subscriptions[command.SlotID]; !exists && len(subscriptions) >= 10 {
			sendError(command.SlotID, "maximum of 10 chart subscriptions reached")
			continue
		}

		events, unsubscribe, err := s.charts.Subscribe(strings.TrimSpace(command.InstrumentID), strings.TrimSpace(command.Interval))
		if err != nil {
			sendError(command.SlotID, err.Error())
			continue
		}
		if previous, ok := subscriptions[command.SlotID]; ok {
			previous.cancel()
			previous.unsubscribe()
		}
		subCtx, subCancel := context.WithCancel(ctx)
		subscription := chartSubscription{cancel: subCancel, unsubscribe: unsubscribe}
		subscriptions[command.SlotID] = subscription

		go func(command chartCommand, events <-chan marketcharts.Event) {
			snapshot, err := s.charts.Snapshot(subCtx, command.InstrumentID, command.Interval, 300)
			if err != nil {
				if !errors.Is(err, context.Canceled) {
					sendError(command.SlotID, err.Error())
				}
				return
			}
			if !send(map[string]any{
				"type":       "snapshot",
				"slot_id":    command.SlotID,
				"instrument": snapshot.Instrument,
				"interval":   snapshot.Interval,
				"candles":    snapshot.Candles,
			}) {
				return
			}
			for {
				select {
				case <-subCtx.Done():
					return
				case event := <-events:
					message := map[string]any{
						"type":    event.Type,
						"slot_id": command.SlotID,
					}
					if event.Status != "" {
						message["status"] = event.Status
					}
					if event.Error != "" {
						message["error"] = event.Error
					}
					if event.Candle != nil {
						message["candle"] = event.Candle
					}
					if !send(message) {
						return
					}
				}
			}
		}(command, events)
	}
}

func chartSocketWriter(ctx context.Context, conn *websocket.Conn, outgoing <-chan any, done chan<- struct{}) {
	defer close(done)
	ping := time.NewTicker(30 * time.Second)
	defer ping.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case value := <-outgoing:
			writeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			err := wsjson.Write(writeCtx, conn, value)
			cancel()
			if err != nil {
				return
			}
		case <-ping.C:
			pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			err := conn.Ping(pingCtx)
			cancel()
			if err != nil {
				return
			}
		}
	}
}
