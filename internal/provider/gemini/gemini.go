// Package gemini implements Google's native GenerateContent API.
package gemini

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"reasonix/internal/netclient"
	"reasonix/internal/provider"
)

func init() {
	provider.Register("gemini", New)
}

// New builds a native Gemini GenerateContent provider from the resolved config.
func New(cfg provider.Config) (provider.Provider, error) {
	if strings.TrimSpace(cfg.BaseURL) == "" {
		return nil, fmt.Errorf("gemini: base_url is required for provider %q", cfg.Name)
	}
	if strings.TrimSpace(cfg.Model) == "" {
		return nil, fmt.Errorf("gemini: model is required for provider %q", cfg.Name)
	}
	httpClient := &http.Client{}
	if proxy, ok := cfg.Extra["proxy_spec"].(netclient.ProxySpec); ok {
		if built, err := netclient.NewHTTPClient(proxy, netclient.TransportOptions{}); err == nil {
			httpClient = built
		}
	}
	keyEnv, _ := cfg.Extra["api_key_env"].(string)
	keySource, _ := cfg.Extra["api_key_source"].(string)
	headers, _ := cfg.Extra["headers"].(map[string]string)
	effort, _ := cfg.Extra["effort"].(string)
	thinking, _ := cfg.Extra["thinking"].(string)
	thinkingBudget, _ := cfg.Extra["thinking_budget"].(int)
	return &client{
		name: cfg.Name, baseURL: strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/"),
		model: cfg.Model, apiKey: cfg.APIKey, keyEnv: keyEnv, keySource: keySource,
		headers: headers, effort: strings.ToLower(strings.TrimSpace(effort)),
		thinking: strings.ToLower(strings.TrimSpace(thinking)), thinkingBudget: thinkingBudget,
		http: httpClient,
	}, nil
}

type client struct {
	name, baseURL, model      string
	apiKey, keyEnv, keySource string
	headers                   map[string]string
	effort, thinking          string
	thinkingBudget            int
	http                      *http.Client
}

func (c *client) Name() string { return c.name }

func (c *client) Stream(ctx context.Context, req provider.Request) (<-chan provider.Chunk, error) {
	body, err := c.buildRequest(req)
	if err != nil {
		return nil, err
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("gemini: marshal request: %w", err)
	}
	stream := streamRequested(req)
	endpoint := c.endpoint(stream)
	newRequest := func(ctx context.Context) (*http.Request, error) {
		httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
		if err != nil {
			return nil, err
		}
		httpReq.Header.Set("Content-Type", "application/json")
		httpReq.Header.Set("Accept", acceptHeader(stream))
		if strings.TrimSpace(c.apiKey) != "" {
			httpReq.Header.Set("x-goog-api-key", c.apiKey)
		}
		for name, value := range c.headers {
			if strings.TrimSpace(name) != "" && strings.TrimSpace(value) != "" && !strings.EqualFold(name, "x-goog-api-key") {
				httpReq.Header.Set(name, value)
			}
		}
		return httpReq, nil
	}
	resp, err := provider.SendWithRetry(ctx, c.http, provider.SendOptions{
		Provider: c.name, KeyEnv: c.keyEnv, KeySource: c.keySource,
		KeyPresent: strings.TrimSpace(c.apiKey) != "",
	}, newRequest)
	if err != nil {
		return nil, err
	}
	out := make(chan provider.Chunk, 16)
	if stream || isEventStream(resp) {
		go c.readSSE(ctx, resp, out)
	} else {
		go c.readJSON(ctx, resp, out)
	}
	return out, nil
}

func (c *client) endpoint(stream bool) string {
	method := ":generateContent"
	query := ""
	if stream {
		method = ":streamGenerateContent"
		query = "?alt=sse"
	}
	return c.baseURL + "/models/" + url.PathEscape(c.model) + method + query
}

func streamRequested(req provider.Request) bool { return req.Stream == nil || *req.Stream }

func acceptHeader(stream bool) string {
	if stream {
		return "text/event-stream"
	}
	return "application/json"
}

func isEventStream(resp *http.Response) bool {
	return resp != nil && strings.Contains(strings.ToLower(resp.Header.Get("Content-Type")), "text/event-stream")
}

type request struct {
	SystemInstruction *content         `json:"systemInstruction,omitempty"`
	Contents          []content        `json:"contents"`
	Tools             []tool           `json:"tools,omitempty"`
	GenerationConfig  generationConfig `json:"generationConfig,omitempty"`
}

type content struct {
	Role  string `json:"role,omitempty"`
	Parts []part `json:"parts"`
}

type part struct {
	Text             string            `json:"text,omitempty"`
	InlineData       *inlineData       `json:"inlineData,omitempty"`
	FileData         *fileData         `json:"fileData,omitempty"`
	FunctionCall     *functionCall     `json:"functionCall,omitempty"`
	FunctionResponse *functionResponse `json:"functionResponse,omitempty"`
	ThoughtSignature string            `json:"thoughtSignature,omitempty"`
}

type inlineData struct {
	MIMEType string `json:"mimeType"`
	Data     string `json:"data"`
}

type fileData struct {
	MIMEType string `json:"mimeType"`
	FileURI  string `json:"fileUri"`
}

type functionCall struct {
	Name string         `json:"name"`
	Args map[string]any `json:"args,omitempty"`
}

type functionResponse struct {
	Name     string         `json:"name"`
	Response map[string]any `json:"response"`
}

type tool struct {
	FunctionDeclarations []functionDeclaration `json:"functionDeclarations,omitempty"`
}

type functionDeclaration struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
}

type generationConfig struct {
	Temperature      *float64        `json:"temperature,omitempty"`
	MaxOutputTokens  int             `json:"maxOutputTokens,omitempty"`
	ResponseMIMEType string          `json:"responseMimeType,omitempty"`
	ThinkingConfig   *thinkingConfig `json:"thinkingConfig,omitempty"`
}

type thinkingConfig struct {
	IncludeThoughts bool `json:"includeThoughts,omitempty"`
	ThinkingBudget  *int `json:"thinkingBudget,omitempty"`
}

func (c *client) buildRequest(req provider.Request) (request, error) {
	var out request
	for _, message := range provider.SanitizeToolPairing(provider.ModelMessages(req.Messages)) {
		switch message.Role {
		case provider.RoleSystem:
			parts, err := messageParts(message)
			if err != nil {
				return request{}, err
			}
			for _, p := range parts {
				if p.InlineData != nil || p.FileData != nil {
					return request{}, errors.New("gemini: system messages cannot contain images")
				}
				out.SystemInstruction = appendContentPart(out.SystemInstruction, p)
			}
		case provider.RoleUser:
			parts, err := messageParts(message)
			if err != nil {
				return request{}, err
			}
			content := content{Role: "user", Parts: parts}
			out.Contents = append(out.Contents, content)
		case provider.RoleAssistant:
			content := content{Role: "model"}
			if message.Content != "" {
				content.Parts = append(content.Parts, part{Text: message.Content})
			}
			for _, call := range message.ToolCalls {
				args := map[string]any{}
				if call.Arguments != "" && json.Unmarshal([]byte(call.Arguments), &args) != nil {
					return request{}, fmt.Errorf("gemini: invalid function-call arguments for %q", call.Name)
				}
				content.Parts = append(content.Parts, part{
					FunctionCall:     &functionCall{Name: call.Name, Args: args},
					ThoughtSignature: call.ThoughtSignature,
				})
			}
			if len(content.Parts) > 0 {
				out.Contents = append(out.Contents, content)
			}
		case provider.RoleTool:
			response := map[string]any{"content": message.Content}
			out.Contents = append(out.Contents, content{Role: "user", Parts: []part{{FunctionResponse: &functionResponse{Name: message.Name, Response: response}}}})
		}
	}
	if len(out.Contents) == 0 {
		return request{}, errors.New("gemini: request has no user or model content")
	}
	if req.Temperature != nil {
		out.GenerationConfig.Temperature = req.Temperature
	}
	if req.MaxTokens > 0 {
		out.GenerationConfig.MaxOutputTokens = req.MaxTokens
	}
	if req.ResponseFormat != nil && req.ResponseFormat.Type != "" {
		if req.ResponseFormat.Type != "json_object" {
			return request{}, fmt.Errorf("gemini: unsupported response format %q", req.ResponseFormat.Type)
		}
		out.GenerationConfig.ResponseMIMEType = "application/json"
	}
	if c.thinking == "disabled" || c.effort == "disabled" || c.effort == "none" || c.effort == "off" {
		zero := 0
		out.GenerationConfig.ThinkingConfig = &thinkingConfig{ThinkingBudget: &zero}
	} else if c.thinking != "" || c.effort != "" {
		thinking := &thinkingConfig{IncludeThoughts: true}
		if c.thinkingBudget != 0 {
			thinking.ThinkingBudget = &c.thinkingBudget
		}
		out.GenerationConfig.ThinkingConfig = thinking
	}
	for _, schema := range req.Tools {
		parameters := schema.Parameters
		if len(parameters) == 0 {
			parameters = provider.CanonicalizeSchema(nil)
		}
		out.Tools = append(out.Tools, tool{FunctionDeclarations: []functionDeclaration{{Name: schema.Name, Description: schema.Description, Parameters: parameters}}})
	}
	return out, nil
}

func messageParts(message provider.Message) ([]part, error) {
	parts := make([]part, 0, 1+len(message.Images))
	if message.Content != "" {
		parts = append(parts, part{Text: message.Content})
	}
	for _, image := range message.Images {
		mediaType, data, ok := provider.ParseImageDataURL(image)
		if ok {
			parts = append(parts, part{InlineData: &inlineData{MIMEType: mediaType, Data: data}})
			continue
		}
		parsed, err := url.Parse(strings.TrimSpace(image))
		if err == nil && (parsed.Scheme == "http" || parsed.Scheme == "https") {
			mediaType := imageMIMEType(parsed.Path)
			if mediaType == "" {
				return nil, fmt.Errorf("gemini: image URL %q has no supported image extension", parsed.String())
			}
			parts = append(parts, part{FileData: &fileData{MIMEType: mediaType, FileURI: parsed.String()}})
			continue
		}
		return nil, errors.New("gemini: image must be a base64 data URL or a supported image URL")
	}
	return parts, nil
}

func imageMIMEType(path string) string {
	switch strings.ToLower(filepathExt(path)) {
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".png":
		return "image/png"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	default:
		return ""
	}
}

func filepathExt(path string) string {
	if i := strings.LastIndexByte(path, '.'); i >= 0 {
		return path[i:]
	}
	return ""
}

func appendContentPart(dst *content, p part) *content {
	if dst == nil {
		dst = &content{}
	}
	dst.Parts = append(dst.Parts, p)
	return dst
}

type response struct {
	Candidates    []candidate    `json:"candidates"`
	UsageMetadata *usageMetadata `json:"usageMetadata"`
	Error         *responseError `json:"error"`
}

type candidate struct {
	Content      contentResponse `json:"content"`
	FinishReason string          `json:"finishReason"`
}

type contentResponse struct {
	Parts []responsePart `json:"parts"`
}

type responsePart struct {
	Text             string        `json:"text"`
	Thought          bool          `json:"thought"`
	ThoughtSignature string        `json:"thoughtSignature"`
	FunctionCall     *functionCall `json:"functionCall"`
}

type usageMetadata struct {
	PromptTokenCount     int `json:"promptTokenCount"`
	CandidatesTokenCount int `json:"candidatesTokenCount"`
	TotalTokenCount      int `json:"totalTokenCount"`
	ThoughtsTokenCount   int `json:"thoughtsTokenCount"`
}

type responseError struct {
	Message string `json:"message"`
}

func (c *client) readJSON(ctx context.Context, resp *http.Response, out chan<- provider.Chunk) {
	defer resp.Body.Close()
	defer close(out)
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		c.send(ctx, out, provider.Chunk{Type: provider.ChunkError, Err: err})
		return
	}
	var decoded response
	if err := json.Unmarshal(raw, &decoded); err != nil {
		c.send(ctx, out, provider.Chunk{Type: provider.ChunkError, Err: fmt.Errorf("gemini: decode response: %w", err)})
		return
	}
	if decoded.Error != nil {
		c.send(ctx, out, provider.Chunk{Type: provider.ChunkError, Err: fmt.Errorf("gemini: %s", decoded.Error.Message)})
		return
	}
	c.emitResponse(ctx, out, decoded)
	c.send(ctx, out, provider.Chunk{Type: provider.ChunkDone})
}

func (c *client) readSSE(ctx context.Context, resp *http.Response, out chan<- provider.Chunk) {
	defer resp.Body.Close()
	defer close(out)
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
	var data strings.Builder
	flush := func() bool {
		if data.Len() == 0 {
			return true
		}
		var decoded response
		if err := json.Unmarshal([]byte(data.String()), &decoded); err != nil {
			c.send(ctx, out, provider.Chunk{Type: provider.ChunkError, Err: fmt.Errorf("gemini: decode stream event: %w", err)})
			return false
		}
		data.Reset()
		if decoded.Error != nil {
			c.send(ctx, out, provider.Chunk{Type: provider.ChunkError, Err: fmt.Errorf("gemini: %s", decoded.Error.Message)})
			return false
		}
		c.emitResponse(ctx, out, decoded)
		return true
	}
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "data:") {
			data.WriteString(strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		}
		if line == "" && !flush() {
			return
		}
	}
	if scanner.Err() != nil {
		c.send(ctx, out, provider.Chunk{Type: provider.ChunkError, Err: fmt.Errorf("gemini: read stream: %w", scanner.Err())})
		return
	}
	if !flush() {
		return
	}
	c.send(ctx, out, provider.Chunk{Type: provider.ChunkDone})
}

func (c *client) emitResponse(ctx context.Context, out chan<- provider.Chunk, decoded response) {
	for _, candidate := range decoded.Candidates {
		for _, item := range candidate.Content.Parts {
			if item.FunctionCall != nil {
				args, _ := json.Marshal(item.FunctionCall.Args)
				call := &provider.ToolCall{ID: item.FunctionCall.Name, Name: item.FunctionCall.Name, Arguments: string(args), ThoughtSignature: item.ThoughtSignature}
				c.send(ctx, out, provider.Chunk{Type: provider.ChunkToolCallStart, ToolCall: &provider.ToolCall{ID: call.ID, Name: call.Name}})
				c.send(ctx, out, provider.Chunk{Type: provider.ChunkToolCall, ToolCall: call})
				continue
			}
			if item.Text == "" {
				continue
			}
			kind := provider.ChunkText
			if item.Thought {
				kind = provider.ChunkReasoning
			}
			c.send(ctx, out, provider.Chunk{Type: kind, Text: item.Text, Signature: item.ThoughtSignature})
		}
	}
	if decoded.UsageMetadata != nil {
		u := decoded.UsageMetadata
		usage := &provider.Usage{PromptTokens: u.PromptTokenCount, CompletionTokens: u.CandidatesTokenCount, TotalTokens: u.TotalTokenCount, ReasoningTokens: u.ThoughtsTokenCount}
		if usage.TotalTokens == 0 {
			usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens
		}
		provider.ApplyRequestAttemptCount(ctx, usage)
		c.send(ctx, out, provider.Chunk{Type: provider.ChunkUsage, Usage: usage})
	}
}

func (c *client) send(ctx context.Context, out chan<- provider.Chunk, chunk provider.Chunk) bool {
	select {
	case out <- chunk:
		return true
	case <-ctx.Done():
		return false
	}
}
