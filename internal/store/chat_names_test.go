package store

import (
	"context"
	"testing"
	"time"
)

func TestChatNamesUseCurrentContactsAndLIDMappings(t *testing.T) {
	s := openTestStore(t)
	for _, q := range []string{
		`INSERT INTO whatsmeow_lid_map(lid,pn) VALUES('811','18005550101'),('812','18005550102')`,
		`INSERT INTO whatsmeow_contacts(our_jid,their_jid,full_name,push_name) VALUES('synthetic','18005550101@s.whatsapp.net','Synthetic Saved Name','Synthetic Profile'),('synthetic','812@lid','','Synthetic Linked Name'),('synthetic','813@lid','','Synthetic Unmapped Name'),('synthetic','synthetic@g.us','Wrong member name','')`,
	} {
		if _, err := s.db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	cases := map[string]string{"811@lid": "Synthetic Saved Name", "18005550102@s.whatsapp.net": "Synthetic Linked Name", "813@lid": "Synthetic Unmapped Name", "814@lid": "814", "synthetic@g.us": "Synthetic Group"}
	for jid := range cases {
		cached := "814"
		if jid == "synthetic@g.us" {
			cached = "Synthetic Group"
		}
		if err := s.StoreChat(jid, cached, time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	chats, err := s.ListChats(context.Background(), "", 100, 0, false, "last_message_time")
	if err != nil {
		t.Fatal(err)
	}
	for _, chat := range chats {
		if want, ok := cases[chat.JID]; ok && chat.Name != want {
			t.Errorf("list %s: got %q want %q", chat.JID, chat.Name, want)
		}
	}
	for jid, want := range cases {
		chat, err := s.GetChat(context.Background(), jid, false)
		if err != nil || chat == nil {
			t.Fatalf("get chat: %v", err)
		}
		if chat.Name != want {
			t.Errorf("get %s: got %q want %q", jid, chat.Name, want)
		}
	}
	if _, err := s.db.Exec(`UPDATE whatsmeow_contacts SET full_name='Synthetic Updated Name' WHERE their_jid='18005550101@s.whatsapp.net'`); err != nil {
		t.Fatal(err)
	}
	chat, err := s.GetChat(context.Background(), "811@lid", false)
	if err != nil || chat.Name != "Synthetic Updated Name" {
		t.Fatal("late contact sync not reflected")
	}
	other := openTestStore(t)
	if other.ContactName("811@lid") != "" {
		t.Fatal("contact name crossed account stores")
	}
}
