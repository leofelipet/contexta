package uazapi

import (
	"context"
	"net/http"
)

type SendTextResult struct {
	MessageID string
	Status    string
}

// Media is the /send/media body; File is a URL UAZAPI downloads itself.
type Media struct {
	Number   string `json:"number"`
	Type     string `json:"type"`
	File     string `json:"file"`
	DocName  string `json:"docName,omitempty"`
	Text     string `json:"text,omitempty"`
	Mentions string `json:"mentions,omitempty"`
}

// Pix is the /send/pix-button body.
type Pix struct {
	Number  string `json:"number"`
	PixType string `json:"pixType"`
	PixKey  string `json:"pixKey"`
	PixName string `json:"pixName,omitempty"`
}

// SendText sends text unchanged; number may be digits, a user JID or a group JID.
// mentions is a comma-separated list of numbers or "all" (groups only); empty sends none.
func (c *Client) SendText(ctx context.Context, number, text, mentions string) (SendTextResult, error) {
	return c.send(ctx, "/send/text", struct {
		Number   string `json:"number"`
		Text     string `json:"text"`
		Mentions string `json:"mentions,omitempty"`
	}{number, text, mentions})
}

func (c *Client) SendMedia(ctx context.Context, media Media) (SendTextResult, error) {
	return c.send(ctx, "/send/media", media)
}

func (c *Client) SendPix(ctx context.Context, pix Pix) (SendTextResult, error) {
	return c.send(ctx, "/send/pix-button", pix)
}

func (c *Client) send(ctx context.Context, path string, body any) (SendTextResult, error) {
	var response struct {
		MessageID      string `json:"messageid"`
		MessageIDCamel string `json:"messageId"`
		Status         string `json:"status"`
	}
	if err := c.do(ctx, http.MethodPost, path, body, &response); err != nil {
		return SendTextResult{}, err
	}
	id := response.MessageID
	if id == "" {
		id = response.MessageIDCamel
	}
	return SendTextResult{MessageID: id, Status: response.Status}, nil
}
