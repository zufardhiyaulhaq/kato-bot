package lark

import (
	"testing"

	"github.com/zufardhiyaulhaq/kato-bot/internal/core"
)

func TestDecodeMessageAlwaysListsUseCases(t *testing.T) {
	in := decodeMessage("oc_chat", "om_user_msg")
	lu, ok := in.(core.ListUseCases)
	if !ok {
		t.Fatalf("got %T", in)
	}
	if lu.Reply.ChatID != "oc_chat" || lu.Reply.InReplyTo != "om_user_msg" {
		t.Fatalf("reply = %+v", lu.Reply)
	}
}

func TestDecodeCardActionPick(t *testing.T) {
	raw := []byte(`{
		"action": {"value": {"action":"pick","usecase":"pod-crashloop"}},
		"context": {"open_chat_id":"oc_1","open_message_id":"om_card"}
	}`)
	in, err := decodeCardAction(raw)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	pick, ok := in.(core.PickUseCase)
	if !ok {
		t.Fatalf("got %T", in)
	}
	if pick.Name != "pod-crashloop" || pick.Reply.MessageID != "om_card" || pick.Reply.ChatID != "oc_1" {
		t.Fatalf("pick = %+v", pick)
	}
}

func TestDecodeCardActionRun(t *testing.T) {
	raw := []byte(`{
		"action": {
			"value": {"action":"run","usecase":"pod-crashloop"},
			"form_value": {"namespace":"payments","pod":"api-xyz"}
		},
		"context": {"open_chat_id":"oc_1","open_message_id":"om_card"}
	}`)
	in, err := decodeCardAction(raw)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	sub, ok := in.(core.SubmitForm)
	if !ok {
		t.Fatalf("got %T", in)
	}
	if sub.Name != "pod-crashloop" || sub.Inputs["namespace"] != "payments" || sub.Inputs["pod"] != "api-xyz" {
		t.Fatalf("submit = %+v", sub)
	}
	if sub.Reply.MessageID != "om_card" {
		t.Fatalf("reply = %+v", sub.Reply)
	}
}

func TestDecodeCardActionUnknown(t *testing.T) {
	raw := []byte(`{"action":{"value":{"action":"frobnicate"}},"context":{}}`)
	if _, err := decodeCardAction(raw); err == nil {
		t.Fatal("expected error on unknown action")
	}
}
