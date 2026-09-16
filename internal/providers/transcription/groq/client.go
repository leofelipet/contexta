package groq

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"strings"
	"time"

	"github.com/leofelipet/contexta/internal/transcription"
)

const defaultEndpoint = "https://api.groq.com/openai/v1/audio/transcriptions"

type Client struct {
	apiKey   string
	model    string
	endpoint string
	http     *http.Client
}

func NewClient(apiKey, model string) *Client {
	return &Client{
		apiKey: apiKey, model: model, endpoint: defaultEndpoint,
		http: &http.Client{Timeout: 90 * time.Second},
	}
}

func (c *Client) Transcribe(ctx context.Context, audio transcription.Audio, language string) (transcription.Result, error) {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	header := make(textproto.MIMEHeader)
	header.Set("Content-Disposition", fmt.Sprintf(`form-data; name="file"; filename="%s"`, audio.FileName))
	header.Set("Content-Type", audio.MIMEType)
	part, err := writer.CreatePart(header)
	if err != nil {
		return transcription.Result{}, fmt.Errorf("create Groq audio part: %w", err)
	}
	if _, err := part.Write(audio.Data); err != nil {
		return transcription.Result{}, fmt.Errorf("write Groq audio part: %w", err)
	}
	for key, value := range map[string]string{
		"model": c.model, "language": language, "response_format": "verbose_json", "temperature": "0",
	} {
		if err := writer.WriteField(key, value); err != nil {
			return transcription.Result{}, fmt.Errorf("write Groq field: %w", err)
		}
	}
	if err := writer.Close(); err != nil {
		return transcription.Result{}, fmt.Errorf("close Groq request: %w", err)
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, &body)
	if err != nil {
		return transcription.Result{}, fmt.Errorf("create Groq request: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+c.apiKey)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	response, err := c.http.Do(request)
	if err != nil {
		return transcription.Result{}, fmt.Errorf("call Groq: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 1<<20))
		return transcription.Result{}, &transcription.RemoteError{
			Provider: "Groq", StatusCode: response.StatusCode,
			Delay: transcription.RetryAfter(response.Header.Get("Retry-After"), time.Now()),
		}
	}
	var output struct {
		Text     string `json:"text"`
		Language string `json:"language"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&output); err != nil {
		return transcription.Result{}, fmt.Errorf("decode Groq response: %w", err)
	}
	if strings.TrimSpace(output.Text) == "" {
		return transcription.Result{}, fmt.Errorf("Groq returned an empty transcription")
	}
	return transcription.Result{Text: strings.TrimSpace(output.Text), Language: output.Language, Model: c.model}, nil
}
