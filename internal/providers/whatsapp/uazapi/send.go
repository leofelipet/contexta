package uazapi

import (
	"context"
	"net/http"
)

type SendTextResult struct {
	MessageID string
	Status    string
}

// SendText sends text unchanged; number may be digits or a user JID.
func (c *Client) SendText(ctx context.Context, number, text string) (SendTextResult, error) {
	body := struct {
		Number string `json:"number"`
		Text   string `json:"text"`
	}{number, text}
	var response struct {
		MessageID      string `json:"messageid"`
		MessageIDCamel string `json:"messageId"`
		Status         string `json:"status"`
	}
	if err := c.do(ctx, http.MethodPost, "/send/text", body, &response); err != nil {
		return SendTextResult{}, err
	}
	id := response.MessageID
	if id == "" {
		id = response.MessageIDCamel
	}
	return SendTextResult{MessageID: id, Status: response.Status}, nil
}
