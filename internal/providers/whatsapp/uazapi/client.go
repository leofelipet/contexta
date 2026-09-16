package uazapi

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strings"
	"time"

	"github.com/leofelipet/contexta/internal/chats"
	"github.com/leofelipet/contexta/internal/transcription"
)

type Client struct {
	baseURL string
	token   string
	http    *http.Client
}

type InstanceStatus struct {
	Instance struct {
		ID          string `json:"id"`
		Name        string `json:"name"`
		Status      string `json:"status"`
		ProfileName string `json:"profileName"`
	} `json:"instance"`
	Status struct {
		Connected bool `json:"connected"`
		LoggedIn  bool `json:"loggedIn"`
	} `json:"status"`
}

type Webhook struct {
	Enabled bool     `json:"enabled"`
	URL     string   `json:"url"`
	Events  []string `json:"events"`
}

func NewClient(baseURL, token string) *Client {
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		token:   token,
		http:    &http.Client{Timeout: 15 * time.Second},
	}
}

func (c *Client) Status(ctx context.Context) (InstanceStatus, error) {
	var status InstanceStatus
	if err := c.do(ctx, http.MethodGet, "/instance/status", nil, &status); err != nil {
		return InstanceStatus{}, err
	}
	return status, nil
}

func (c *Client) ConfigureWebhook(ctx context.Context, callbackURL string) error {
	payload := map[string]any{
		"enabled":             true,
		"url":                 callbackURL,
		"events":              []string{"connection", "history", "messages", "messages_update"},
		"excludeMessages":     []string{},
		"addUrlEvents":        false,
		"addUrlTypesMessages": false,
	}
	return c.do(ctx, http.MethodPost, "/webhook", payload, nil)
}

func (c *Client) DownloadAudio(ctx context.Context, messageID string, maxBytes int64) (transcription.Audio, error) {
	body := map[string]any{
		"id": messageID, "return_base64": true, "return_link": false,
		"generate_mp3": false, "transcribe": false,
	}
	var response struct {
		Base64Data string `json:"base64Data"`
		MIMEType   string `json:"mimetype"`
	}
	if err := c.doLimit(ctx, http.MethodPost, "/message/download", body, &response, maxBytes*2); err != nil {
		return transcription.Audio{}, err
	}
	mediaType, _, err := mime.ParseMediaType(response.MIMEType)
	if err != nil || !strings.HasPrefix(strings.ToLower(mediaType), "audio/") {
		return transcription.Audio{}, fmt.Errorf("UAZAPI returned non-audio media")
	}
	encoded := response.Base64Data
	if index := strings.IndexByte(encoded, ','); strings.HasPrefix(encoded, "data:") && index >= 0 {
		encoded = encoded[index+1:]
	}
	data, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return transcription.Audio{}, fmt.Errorf("decode UAZAPI audio: %w", err)
	}
	if int64(len(data)) > maxBytes {
		return transcription.Audio{}, fmt.Errorf("UAZAPI audio exceeds size limit")
	}
	return transcription.Audio{Data: data, MIMEType: mediaType, FileName: audioFileName(mediaType)}, nil
}

func audioFileName(mediaType string) string {
	extensions := map[string]string{
		"audio/flac": ".flac", "audio/mpeg": ".mp3", "audio/mp4": ".m4a",
		"audio/ogg": ".ogg", "audio/wav": ".wav", "audio/x-wav": ".wav", "audio/webm": ".webm",
	}
	if extension := extensions[strings.ToLower(mediaType)]; extension != "" {
		return "audio" + extension
	}
	return "audio.ogg"
}

func (c *Client) FindChats(ctx context.Context) ([]chats.Profile, error) {
	const limit = 100
	var result []chats.Profile
	for offset := 0; ; offset += limit {
		var response struct {
			Chats []struct {
				JID         string `json:"wa_chatid"`
				LID         string `json:"wa_chatlid"`
				Name        string `json:"name"`
				PushName    string `json:"wa_name"`
				ContactName string `json:"wa_contactName"`
				Phone       string `json:"phone"`
				Group       bool   `json:"wa_isGroup"`
			} `json:"chats"`
			Pagination struct {
				Total int `json:"totalRecords"`
			} `json:"pagination"`
		}
		request := map[string]any{"sort": "-wa_lastMsgTimestamp", "limit": limit, "offset": offset}
		if err := c.doLimit(ctx, http.MethodPost, "/chat/find", request, &response, 8<<20); err != nil {
			return nil, err
		}
		for _, chat := range response.Chats {
			result = append(result, chats.Profile{
				JID: chat.JID, LID: chat.LID, Name: chat.Name, PushName: chat.PushName,
				ContactName: chat.ContactName, Phone: chat.Phone, Group: chat.Group,
			})
		}
		if len(response.Chats) == 0 || offset+len(response.Chats) >= response.Pagination.Total {
			return result, nil
		}
	}
}

func (c *Client) Webhooks(ctx context.Context) ([]Webhook, error) {
	var webhooks []Webhook
	if err := c.do(ctx, http.MethodGet, "/webhook", nil, &webhooks); err != nil {
		return nil, err
	}
	return webhooks, nil
}

func (c *Client) do(ctx context.Context, method, path string, body any, output any) error {
	return c.doLimit(ctx, method, path, body, output, 1<<20)
}

func (c *Client) doLimit(ctx context.Context, method, path string, body any, output any, responseLimit int64) error {
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("encode UAZAPI request: %w", err)
		}
		reader = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reader)
	if err != nil {
		return fmt.Errorf("create UAZAPI request: %w", err)
	}
	req.Header.Set("token", c.token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	response, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("call UAZAPI: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 1<<20))
		return &transcription.RemoteError{
			Provider: "UAZAPI", StatusCode: response.StatusCode,
			Delay: transcription.RetryAfter(response.Header.Get("Retry-After"), time.Now()),
		}
	}
	if output == nil {
		_, _ = io.Copy(io.Discard, response.Body)
		return nil
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, responseLimit)).Decode(output); err != nil {
		return fmt.Errorf("decode UAZAPI response: %w", err)
	}
	return nil
}
