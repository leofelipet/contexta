package chats

import "context"

type Profile struct {
	JID         string
	LID         string
	Name        string
	PushName    string
	ContactName string
	Phone       string
	Group       bool
}

type Store interface {
	SyncChats(context.Context, string, []Profile) (int, error)
}
