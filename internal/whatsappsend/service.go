package whatsappsend

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/leofelipet/contexta/internal/providers/whatsapp/uazapi"
)

const MaxTextRunes = 4096

var (
	ErrInvalidRecipient = errors.New("invalid recipient")
	ErrInvalidText      = errors.New("invalid text")
)

type Sender interface {
	SendText(ctx context.Context, number, text string) (uazapi.SendTextResult, error)
}

type Result struct {
	RecipientJID string `json:"recipient_jid"`
	MessageID    string `json:"message_id"`
	Status       string `json:"status"`
}

type Service struct {
	sender Sender
}

func NewService(sender Sender) *Service {
	return &Service{sender: sender}
}

// NormalizeRecipient accepts a phone number or a <digits>@s.whatsapp.net JID and returns the canonical user JID.
func NormalizeRecipient(value string) (string, error) {
	value = strings.TrimSpace(value)
	digits := value
	if at := strings.IndexByte(value, '@'); at >= 0 {
		if value[at+1:] != "s.whatsapp.net" {
			return "", fmt.Errorf("%w: only individual contacts (@s.whatsapp.net) are allowed", ErrInvalidRecipient)
		}
		digits = value[:at]
	} else {
		digits = strings.NewReplacer("+", "", " ", "", "-", "", "(", "", ")", "", ".", "").Replace(value)
	}
	if len(digits) < 8 || len(digits) > 15 {
		return "", fmt.Errorf("%w: phone number must have 8 to 15 digits", ErrInvalidRecipient)
	}
	for _, r := range digits {
		if r < '0' || r > '9' {
			return "", fmt.Errorf("%w: phone number must contain only digits", ErrInvalidRecipient)
		}
	}
	return digits + "@s.whatsapp.net", nil
}

func ValidateText(text string) error {
	switch {
	case strings.TrimSpace(text) == "":
		return fmt.Errorf("%w: text is required", ErrInvalidText)
	case utf8.RuneCountInString(text) > MaxTextRunes:
		return fmt.Errorf("%w: text exceeds %d characters", ErrInvalidText, MaxTextRunes)
	case strings.Contains(text, "{{"):
		// UAZAPI expands {{placeholders}}, so the delivered text would differ from the approved one.
		return fmt.Errorf("%w: text must not contain {{", ErrInvalidText)
	}
	return nil
}

// Send validates and delivers text unchanged; Result.RecipientJID is set even on sender failure.
func (s *Service) Send(ctx context.Context, recipient, text string) (Result, error) {
	jid, err := NormalizeRecipient(recipient)
	if err != nil {
		return Result{}, err
	}
	if err := ValidateText(text); err != nil {
		return Result{RecipientJID: jid}, err
	}
	sent, err := s.sender.SendText(ctx, jid, text)
	if err != nil {
		return Result{RecipientJID: jid}, err
	}
	return Result{RecipientJID: jid, MessageID: sent.MessageID, Status: sent.Status}, nil
}
