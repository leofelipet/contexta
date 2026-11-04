package whatsappsend

import (
	"context"
	"errors"
	"fmt"
	"mime"
	"net/mail"
	"net/url"
	"path"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/leofelipet/contexta/internal/providers/whatsapp/uazapi"
)

const (
	MaxTextRunes   = 4096
	maxURLLength   = 2048
	maxNameRunes   = 255
	groupJIDSuffix = "@g.us"
)

var (
	ErrInvalidRecipient = errors.New("invalid recipient")
	ErrInvalidText      = errors.New("invalid text")
	ErrInvalidMedia     = errors.New("invalid media")
	ErrInvalidPix       = errors.New("invalid pix")

	MediaTypes = []string{"document", "image", "video", "audio"}
	PixTypes   = []string{"CPF", "CNPJ", "PHONE", "EMAIL", "EVP"}

	evpPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
)

type Sender interface {
	SendText(ctx context.Context, number, text, mentions string) (uazapi.SendTextResult, error)
	SendMedia(ctx context.Context, media uazapi.Media) (uazapi.SendTextResult, error)
	SendPix(ctx context.Context, pix uazapi.Pix) (uazapi.SendTextResult, error)
}

type Result struct {
	RecipientJID string `json:"recipient_jid"`
	MessageID    string `json:"message_id"`
	Status       string `json:"status"`
	PixKey       string `json:"pix_key,omitempty"`
}

type MediaParams struct {
	Type     string
	URL      string
	DocName  string
	Caption  string
	Mentions []string
}

type PixParams struct {
	Type string
	Key  string
	Name string
}

type Service struct {
	sender Sender
}

func NewService(sender Sender) *Service {
	return &Service{sender: sender}
}

// IsRejection reports whether err is a validation failure (nothing was sent).
func IsRejection(err error) bool {
	return errors.Is(err, ErrInvalidRecipient) || errors.Is(err, ErrInvalidText) ||
		errors.Is(err, ErrInvalidMedia) || errors.Is(err, ErrInvalidPix)
}

// NormalizeRecipient accepts a phone number, a <digits>@s.whatsapp.net JID or a group JID (<id>@g.us)
// and returns the canonical JID.
func NormalizeRecipient(value string) (string, error) {
	value = strings.TrimSpace(value)
	if local, ok := strings.CutSuffix(value, groupJIDSuffix); ok {
		// Modern group IDs are digits; legacy ones are <creator>-<timestamp>.
		if len(local) < 10 || len(local) > 40 || strings.Trim(local, "0123456789-") != "" || strings.HasPrefix(local, "-") || strings.HasSuffix(local, "-") {
			return "", fmt.Errorf("%w: malformed group JID", ErrInvalidRecipient)
		}
		return value, nil
	}
	digits := value
	if at := strings.IndexByte(value, '@'); at >= 0 {
		if value[at+1:] != "s.whatsapp.net" {
			return "", fmt.Errorf("%w: only contacts (@s.whatsapp.net) and groups (@g.us) are allowed", ErrInvalidRecipient)
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

// normalizeMentions returns UAZAPI's comma-separated mentions value; mentions are only valid in groups.
func normalizeMentions(jid string, mentions []string) (string, error) {
	if len(mentions) == 0 {
		return "", nil
	}
	if !strings.HasSuffix(jid, groupJIDSuffix) {
		return "", fmt.Errorf("%w: mentions are only allowed when sending to a group", ErrInvalidRecipient)
	}
	numbers := make([]string, 0, len(mentions))
	for _, mention := range mentions {
		if strings.EqualFold(strings.TrimSpace(mention), "all") {
			if len(mentions) != 1 {
				return "", fmt.Errorf("%w: mention \"all\" must be used alone", ErrInvalidRecipient)
			}
			return "all", nil
		}
		user, err := NormalizeRecipient(mention)
		if err != nil || strings.HasSuffix(user, groupJIDSuffix) {
			return "", fmt.Errorf("%w: mention %q must be a phone number", ErrInvalidRecipient, mention)
		}
		numbers = append(numbers, strings.TrimSuffix(user, "@s.whatsapp.net"))
	}
	return strings.Join(numbers, ","), nil
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
func (s *Service) Send(ctx context.Context, recipient, text string, mentions []string) (Result, error) {
	jid, err := NormalizeRecipient(recipient)
	if err != nil {
		return Result{}, err
	}
	mentionsValue, err := normalizeMentions(jid, mentions)
	if err != nil {
		return Result{RecipientJID: jid}, err
	}
	if err := ValidateText(text); err != nil {
		return Result{RecipientJID: jid}, err
	}
	return deliver(jid)(s.sender.SendText(ctx, jid, text, mentionsValue))
}

// SendMedia sends a file that UAZAPI downloads from params.URL, with an optional caption.
func (s *Service) SendMedia(ctx context.Context, recipient string, params MediaParams) (Result, error) {
	jid, err := NormalizeRecipient(recipient)
	if err != nil {
		return Result{}, err
	}
	mentionsValue, err := normalizeMentions(jid, params.Mentions)
	if err != nil {
		return Result{RecipientJID: jid}, err
	}
	if err := validateMedia(params); err != nil {
		return Result{RecipientJID: jid}, err
	}
	return deliver(jid)(s.sender.SendMedia(ctx, uazapi.Media{
		Number: jid, Type: params.Type, File: params.URL, DocName: params.DocName, Text: params.Caption, Mentions: mentionsValue,
		Mimetype: mediaMimetype(params.DocName, params.URL),
	}))
}

// SendPix sends WhatsApp's native PIX button; the key is normalized to its canonical form first.
func (s *Service) SendPix(ctx context.Context, recipient string, params PixParams) (Result, error) {
	jid, err := NormalizeRecipient(recipient)
	if err != nil {
		return Result{}, err
	}
	pixType, key, err := NormalizePixKey(params.Type, params.Key)
	if err != nil {
		return Result{RecipientJID: jid}, err
	}
	name := strings.TrimSpace(params.Name)
	if utf8.RuneCountInString(name) > maxNameRunes || strings.Contains(name, "{{") {
		return Result{RecipientJID: jid}, fmt.Errorf("%w: name must be at most %d characters without {{", ErrInvalidPix, maxNameRunes)
	}
	result, err := deliver(jid)(s.sender.SendPix(ctx, uazapi.Pix{Number: jid, PixType: pixType, PixKey: key, PixName: name}))
	result.PixKey = key
	return result, err
}

func deliver(jid string) func(uazapi.SendTextResult, error) (Result, error) {
	return func(sent uazapi.SendTextResult, err error) (Result, error) {
		if err != nil {
			return Result{RecipientJID: jid}, err
		}
		return Result{RecipientJID: jid, MessageID: sent.MessageID, Status: sent.Status}, nil
	}
}

// officeMimetypes covers common files missing from Go's builtin table (the Alpine image has no /etc/mime.types).
var officeMimetypes = map[string]string{
	".doc": "application/msword", ".docx": "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
	".xls": "application/vnd.ms-excel", ".xlsx": "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
	".ppt": "application/vnd.ms-powerpoint", ".pptx": "application/vnd.openxmlformats-officedocument.presentationml.presentation",
	".csv": "text/csv", ".txt": "text/plain", ".zip": "application/zip", ".mp3": "audio/mpeg", ".ogg": "audio/ogg", ".mp4": "video/mp4",
}

// mediaMimetype guesses the MIME type from doc_name, then the URL path, so UAZAPI does not
// rely on the remote server's Content-Type (which may carry odd parameters). Empty lets UAZAPI detect it.
func mediaMimetype(docName, rawURL string) string {
	extensions := []string{path.Ext(docName)}
	if parsed, err := url.Parse(rawURL); err == nil {
		extensions = append(extensions, path.Ext(parsed.Path))
	}
	for _, extension := range extensions {
		extension = strings.ToLower(extension)
		if extension == "" {
			continue
		}
		if known, ok := officeMimetypes[extension]; ok {
			return known
		}
		if guessed, _, err := mime.ParseMediaType(mime.TypeByExtension(extension)); err == nil {
			return guessed
		}
	}
	return ""
}

func validateMedia(params MediaParams) error {
	if !slices.Contains(MediaTypes, params.Type) {
		return fmt.Errorf("%w: type must be one of %s", ErrInvalidMedia, strings.Join(MediaTypes, ", "))
	}
	parsed, err := url.Parse(params.URL)
	if err != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.Host == "" || len(params.URL) > maxURLLength {
		return fmt.Errorf("%w: url must be an http(s) link of at most %d characters", ErrInvalidMedia, maxURLLength)
	}
	if params.DocName != "" && (params.Type != "document" || utf8.RuneCountInString(params.DocName) > maxNameRunes) {
		return fmt.Errorf("%w: doc_name is only for documents, at most %d characters", ErrInvalidMedia, maxNameRunes)
	}
	if params.Caption != "" {
		if params.Type == "audio" {
			return fmt.Errorf("%w: audio does not take a caption", ErrInvalidMedia)
		}
		return ValidateText(params.Caption)
	}
	return nil
}

// NormalizePixKey validates key against pixType and returns both in canonical form.
func NormalizePixKey(pixType, key string) (string, string, error) {
	pixType = strings.ToUpper(strings.TrimSpace(pixType))
	key = strings.TrimSpace(key)
	strip := func(value, chars string) string {
		return strings.Map(func(r rune) rune {
			if strings.ContainsRune(chars, r) {
				return -1
			}
			return r
		}, value)
	}
	invalid := func(reason string) (string, string, error) {
		return "", "", fmt.Errorf("%w: %s", ErrInvalidPix, reason)
	}
	switch pixType {
	case "CPF":
		key = strip(key, ".- ")
		if !validCPF(key) {
			return invalid("CPF must have 11 digits with valid check digits")
		}
	case "CNPJ":
		key = strings.ToUpper(strip(key, ".-/ "))
		if !validCNPJ(key) {
			return invalid("CNPJ must have 14 characters with valid check digits")
		}
	case "PHONE":
		// PIX phone keys are Brazilian numbers in +55DDNNNNNNNNN form.
		digits := strip(key, "+ -().")
		if !strings.HasPrefix(digits, "55") || len(digits) < 12 || len(digits) > 13 || strings.Trim(digits, "0123456789") != "" {
			return invalid("PHONE must be a Brazilian number like +5511999999999")
		}
		key = "+" + digits
	case "EMAIL":
		address, err := mail.ParseAddress(key)
		if err != nil || address.Address != key || len(key) > 77 {
			return invalid("EMAIL must be a plain email address")
		}
	case "EVP":
		key = strings.ToLower(key)
		if !evpPattern.MatchString(key) {
			return invalid("EVP must be a random key UUID like 123e4567-e89b-12d3-a456-426614174000")
		}
	default:
		return invalid("type must be one of " + strings.Join(PixTypes, ", "))
	}
	return pixType, key, nil
}

func validCPF(digits string) bool {
	if len(digits) != 11 || strings.Trim(digits, "0123456789") != "" || strings.Count(digits, digits[:1]) == 11 {
		return false
	}
	for _, length := range []int{9, 10} {
		sum := 0
		for i := 0; i < length; i++ {
			sum += int(digits[i]-'0') * (length + 1 - i)
		}
		if check := sum * 10 % 11 % 10; check != int(digits[length]-'0') {
			return false
		}
	}
	return true
}

// validCNPJ accepts numeric and alphanumeric (2026+) CNPJs; letters count as ASCII-48.
func validCNPJ(value string) bool {
	if len(value) != 14 || strings.Trim(value[:12], "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZ") != "" ||
		strings.Trim(value[12:], "0123456789") != "" || strings.Count(value, value[:1]) == 14 {
		return false
	}
	for _, length := range []int{12, 13} {
		sum, weight := 0, length-7
		for i := 0; i < length; i++ {
			sum += int(value[i]-'0') * weight
			if weight--; weight < 2 {
				weight = 9
			}
		}
		check := 0
		if r := sum % 11; r >= 2 {
			check = 11 - r
		}
		if check != int(value[length]-'0') {
			return false
		}
	}
	return true
}
