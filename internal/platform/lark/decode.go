package lark

import (
	"encoding/json"
	"fmt"

	"github.com/zufardhiyaulhaq/kato-bot/internal/core"
)

// decodeMessage maps any received user message to a ListUseCases intent (show the
// picker). chatID is the message's chat; userMsgID is the message to reply to.
func decodeMessage(chatID, userMsgID string) core.Intent {
	return core.ListUseCases{Reply: core.Reply{ChatID: chatID, InReplyTo: userMsgID}}
}

// cardActionPayload is the subset of Lark's card.action.trigger event we use. The
// callback context identifies the bot card itself (open_message_id), which is exactly
// what we patch on subsequent renders — so no message-id threading is needed.
type cardActionPayload struct {
	Action struct {
		Value     map[string]any    `json:"value"`
		FormValue map[string]string `json:"form_value"`
	} `json:"action"`
	Context struct {
		OpenChatID    string `json:"open_chat_id"`
		OpenMessageID string `json:"open_message_id"`
	} `json:"context"`
}

// decodeCardAction parses a card.action.trigger payload into a PickUseCase or SubmitForm.
func decodeCardAction(raw []byte) (core.Intent, error) {
	var p cardActionPayload
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, fmt.Errorf("decode card action: %w", err)
	}
	reply := core.Reply{ChatID: p.Context.OpenChatID, MessageID: p.Context.OpenMessageID}
	action, _ := p.Action.Value["action"].(string)
	useCase, _ := p.Action.Value["usecase"].(string)
	switch action {
	case "pick":
		return core.PickUseCase{Reply: reply, Name: useCase}, nil
	case "run":
		inputs := p.Action.FormValue
		if inputs == nil {
			inputs = map[string]string{}
		}
		return core.SubmitForm{Reply: reply, Name: useCase, Inputs: inputs}, nil
	default:
		return nil, fmt.Errorf("unknown card action %q", action)
	}
}
