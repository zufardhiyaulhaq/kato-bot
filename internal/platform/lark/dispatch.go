package lark

import (
	"context"
	"encoding/json"
	"log"
	"strings"
	"sync"
	"time"

	larkcore "github.com/larksuite/oapi-sdk-go/v3/core"
	"github.com/larksuite/oapi-sdk-go/v3/event/dispatcher"
	"github.com/larksuite/oapi-sdk-go/v3/event/dispatcher/callback"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
	larkws "github.com/larksuite/oapi-sdk-go/v3/ws"

	"github.com/zufardhiyaulhaq/kato-bot/internal/core"
)

// defaultMaxConcurrentRuns bounds in-flight kato runs when MaxConcurrent is unset.
const defaultMaxConcurrentRuns = 4

// Adapter wires Lark WebSocket events into core.Core.
type Adapter struct {
	AppID         string
	AppSecret     string
	Core          *core.Core
	RunTimeout    time.Duration
	LogLevel      string // "debug" | "info" | "warn" | "error"; default info
	MaxConcurrent int    // cap on in-flight kato runs; <=0 uses defaultMaxConcurrentRuns
	BaseURL       string // open-platform base URL (e.g. https://open.larksuite.com)

	semOnce sync.Once
	sem     chan struct{}
}

// runSem lazily builds the in-flight-run semaphore (bounded by MaxConcurrent).
func (a *Adapter) runSem() chan struct{} {
	a.semOnce.Do(func() {
		n := a.MaxConcurrent
		if n <= 0 {
			n = defaultMaxConcurrentRuns
		}
		a.sem = make(chan struct{}, n)
	})
	return a.sem
}

// larkLogLevel maps a config log-level string to the SDK's level (default info).
func larkLogLevel(s string) larkcore.LogLevel {
	switch strings.ToLower(s) {
	case "debug":
		return larkcore.LogLevelDebug
	case "warn", "warning":
		return larkcore.LogLevelWarn
	case "error":
		return larkcore.LogLevelError
	default:
		return larkcore.LogLevelInfo
	}
}

// Start opens the Lark WebSocket long-connection and dispatches events into Core.
// It blocks until ctx is cancelled.
//
// VERIFY (event field access): confirmed against oapi-sdk-go v3.9.5:
//   - OnP2MessageReceiveV1 handler receives *larkim.P2MessageReceiveV1.
//     Fields: e.Event.Message.ChatId (*string) and e.Event.Message.MessageId (*string).
//     Both are pointer fields; derefStr handles nil safely.
//   - OnP2CardActionTrigger handler receives *callback.CardActionTriggerEvent (NOT interface{}).
//     Typed fields: event.Event.Action.Value (map[string]interface{}),
//     event.Event.Action.FormValue (map[string]interface{} — note: NOT map[string]string),
//     event.Event.Context.OpenChatID (string), event.Event.Context.OpenMessageID (string).
//     The handler returns (*callback.CardActionTriggerResponse, error); we return nil response + nil error.
//     We re-marshal the typed event into the cardActionPayload JSON shape that decodeCardAction expects,
//     converting FormValue values to strings so the pure decoder remains SDK-free.
func (a *Adapter) Start(ctx context.Context) error {
	handler := dispatcher.NewEventDispatcher("", "").
		OnP2MessageReceiveV1(func(ctx context.Context, e *larkim.P2MessageReceiveV1) error {
			// Event/Message are pointer fields in the SDK; guard against a malformed delivery.
			if e.Event == nil || e.Event.Message == nil {
				log.Printf("message event: missing Event/Message, skipping")
				return nil
			}
			chatID := derefStr(e.Event.Message.ChatId)
			msgID := derefStr(e.Event.Message.MessageId)
			log.Printf("event: message received (chat=%s msg=%s) → showing picker", chatID, msgID)
			a.dispatch(ctx, decodeMessage(chatID, msgID))
			return nil
		}).
		OnP2CardActionTrigger(func(ctx context.Context, event *callback.CardActionTriggerEvent) (*callback.CardActionTriggerResponse, error) {
			// Event/Action/Context are pointer fields in the SDK; guard against a malformed delivery.
			if event.Event == nil || event.Event.Action == nil || event.Event.Context == nil {
				log.Printf("card action event: missing Event/Action/Context, skipping")
				return nil, nil
			}
			// Convert the typed card-action event into the JSON shape our pure decodeCardAction expects.
			// FormValue is map[string]interface{} in the SDK; stringify values for the pure decoder.
			formValueStr := make(map[string]string, len(event.Event.Action.FormValue))
			for k, v := range event.Event.Action.FormValue {
				if s, ok := v.(string); ok {
					formValueStr[k] = s
				} else {
					log.Printf("card form_value: key %q has non-string type %T, skipping", k, v)
				}
			}
			payload := map[string]any{
				"action": map[string]any{
					"value":      event.Event.Action.Value,
					"form_value": formValueStr,
				},
				"context": map[string]any{
					"open_chat_id":    event.Event.Context.OpenChatID,
					"open_message_id": event.Event.Context.OpenMessageID,
				},
			}
			raw, err := json.Marshal(payload)
			if err != nil {
				log.Printf("card action marshal: %v", err)
				return nil, nil
			}
			intent, err := decodeCardAction(raw)
			if err != nil {
				log.Printf("card action decode: %v", err)
				return nil, nil
			}
			log.Printf("event: card action received (%T)", intent)
			a.dispatch(ctx, intent)
			return nil, nil
		})

	cli := larkws.NewClient(a.AppID, a.AppSecret,
		larkws.WithEventHandler(handler),
		larkws.WithDomain(openBaseURL(a.BaseURL)),
		larkws.WithLogLevel(larkLogLevel(a.LogLevel)),
	)
	return cli.Start(ctx)
}

// dispatch runs an intent through Core and, if it produced deferred work (a validated
// run), executes it in a goroutine with an independent timeout so the event callback
// returns immediately (fast-ack).
//
// A submit that would start a kato run is gated by a bounded semaphore: over the cap,
// the user is told kato is busy instead of spawning an unbounded goroutine. The slot is
// acquired before Core.Handle (which paints the "running" card), so an over-cap submit
// repaints to "busy" with no running flicker; it is released when the run completes.
func (a *Adapter) dispatch(ctx context.Context, in core.Intent) {
	if sf, ok := in.(core.SubmitForm); ok {
		select {
		case a.runSem() <- struct{}{}:
			deferred, err := a.Core.Handle(ctx, in)
			if err != nil {
				log.Printf("handle %T: %v", in, err)
			}
			if deferred == nil {
				<-a.runSem() // no run started (validation failed / render error): release now
				return
			}
			go func() {
				defer func() { <-a.runSem() }()
				bg, cancel := context.WithTimeout(context.Background(), a.RunTimeout)
				defer cancel()
				if err := deferred(bg); err != nil {
					log.Printf("deferred run: %v", err)
				}
			}()
		default:
			if err := a.Core.R.RenderError(ctx, sf.Reply,
				"kato is busy (too many runs in flight) — try again shortly"); err != nil {
				log.Printf("render busy: %v", err)
			}
		}
		return
	}

	// Non-run intents (list/pick) are cheap and synchronous; no gating needed.
	if _, err := a.Core.Handle(ctx, in); err != nil {
		log.Printf("handle %T: %v", in, err)
	}
}

func derefStr(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}
