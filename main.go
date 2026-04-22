package main

import (
	"bufio"
	"bytes"
	"context"
	cryptorand "crypto/rand"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
)

const (
	defaultEnvFile    = ".env"
	defaultLLMBaseURL = "https://openrouter.ai/api/v1"
	defaultSystem     = "Ты дружелюбный участник группового чата. Отвечай по-русски, кратко и по делу. Если контекста не хватает, задай уточняющий вопрос."
	defaultReplyProb  = 0.5
	maxHistory        = 18
	maxMessageLength  = 3900
	maxLogTextLength  = 180
)

type config struct {
	TelegramToken string
	LLMBaseURL    string
	LLMAPIKey     string
	LLMModel      string
	SystemPrompt  string
	ReplyProb     float64
}

type bot struct {
	cfg       config
	cfgMu     sync.RWMutex
	http      *http.Client
	me        telegramUser
	histories *chatHistories
}

type chatHistories struct {
	mu    sync.Mutex
	items map[int64][]chatMessage
}

type chatMessage struct {
	Role string
	Text string
}

type telegramResponse[T any] struct {
	OK          bool   `json:"ok"`
	Result      T      `json:"result"`
	Description string `json:"description"`
}

type update struct {
	UpdateID int             `json:"update_id"`
	Message  telegramMessage `json:"message"`
}

type telegramMessage struct {
	MessageID      int              `json:"message_id"`
	Text           string           `json:"text"`
	Chat           telegramChat     `json:"chat"`
	From           telegramUser     `json:"from"`
	ReplyToMessage *telegramMessage `json:"reply_to_message,omitempty"`
}

type telegramChat struct {
	ID   int64  `json:"id"`
	Type string `json:"type"`
}

type telegramUser struct {
	ID        int64  `json:"id"`
	IsBot     bool   `json:"is_bot"`
	FirstName string `json:"first_name"`
	Username  string `json:"username"`
}

type llmRequest struct {
	Model       string       `json:"model"`
	Messages    []llmMessage `json:"messages"`
	Temperature float64      `json:"temperature"`
	MaxTokens   int          `json:"max_tokens"`
}

type llmMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type llmResponse struct {
	Model   string `json:"model"`
	Choices []struct {
		Message llmMessage `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

func main() {
	log.SetFlags(log.LstdFlags | log.Lmicroseconds)

	cfg, err := loadConfig()
	if err != nil {
		log.Fatal(err)
	}

	b := &bot{
		cfg: cfg,
		http: &http.Client{
			Timeout: 90 * time.Second,
		},
		histories: &chatHistories{items: make(map[int64][]chatMessage)},
	}

	ctx := context.Background()
	me, err := b.getMe(ctx)
	if err != nil {
		log.Fatalf("get bot info: %v", err)
	}
	b.me = me

	log.Printf("config loaded: llm_base_url=%s llm_model=%s reply_probability=%.2f", cfg.LLMBaseURL, cfg.LLMModel, cfg.ReplyProb)
	log.Printf("started as @%s id=%d", b.me.Username, b.me.ID)
	if err := b.poll(ctx); err != nil {
		log.Fatal(err)
	}
}

func loadConfig() (config, error) {
	if err := loadEnvFile(defaultEnvFile); err != nil {
		return config{}, err
	}

	replyProb, err := parseOptionalFloat64("REPLY_PROBABILITY", defaultReplyProb)
	if err != nil {
		return config{}, err
	}
	if replyProb < 0 || replyProb > 1 {
		return config{}, fmt.Errorf("REPLY_PROBABILITY must be between 0 and 1")
	}

	cfg := config{
		TelegramToken: strings.TrimSpace(os.Getenv("TELEGRAM_BOT_TOKEN")),
		LLMBaseURL:    strings.TrimRight(firstNonEmpty(os.Getenv("LLM_BASE_URL"), defaultLLMBaseURL), "/"),
		LLMAPIKey:     strings.TrimSpace(os.Getenv("LLM_API_KEY")),
		LLMModel:      strings.TrimSpace(os.Getenv("LLM_MODEL")),
		SystemPrompt:  firstNonEmpty(os.Getenv("SYSTEM_PROMPT"), defaultSystem),
		ReplyProb:     replyProb,
	}

	var missing []string
	if cfg.TelegramToken == "" {
		missing = append(missing, "TELEGRAM_BOT_TOKEN")
	}
	if cfg.LLMAPIKey == "" {
		missing = append(missing, "LLM_API_KEY")
	}
	if cfg.LLMModel == "" {
		missing = append(missing, "LLM_MODEL")
	}
	if len(missing) > 0 {
		return config{}, fmt.Errorf("missing env vars: %s", strings.Join(missing, ", "))
	}

	return cfg, nil
}

func parseOptionalFloat64(name string, defaultValue float64) (float64, error) {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return defaultValue, nil
	}
	value, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return 0, fmt.Errorf("%s must be a number: %w", name, err)
	}
	return value, nil
}

func loadEnvFile(path string) error {
	file, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			log.Printf("env file %s not found, using process environment", path)
			return nil
		}
		return fmt.Errorf("open %s: %w", path, err)
	}
	defer func() {
		if closeErr := file.Close(); closeErr != nil {
			log.Printf("close %s: %v", path, closeErr)
		}
	}()
	log.Printf("loading env from %s", path)

	scanner := bufio.NewScanner(file)
	for lineNumber := 1; scanner.Scan(); lineNumber++ {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")

		key, value, ok := strings.Cut(line, "=")
		if !ok {
			return fmt.Errorf("%s:%d: expected KEY=value", path, lineNumber)
		}

		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if key == "" {
			return fmt.Errorf("%s:%d: empty key", path, lineNumber)
		}
		if _, exists := os.LookupEnv(key); exists {
			continue
		}

		value = strings.TrimSpace(stripInlineComment(value))
		unquoted, err := unquoteEnvValue(value)
		if err != nil {
			return fmt.Errorf("%s:%d: %w", path, lineNumber, err)
		}
		if err := os.Setenv(key, unquoted); err != nil {
			return fmt.Errorf("%s:%d: set %s: %w", path, lineNumber, key, err)
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	return nil
}

func stripInlineComment(value string) string {
	var quote rune
	for i, r := range value {
		switch r {
		case '\'', '"':
			if quote == 0 {
				quote = r
			} else if quote == r {
				quote = 0
			}
		case '#':
			if quote == 0 && (i == 0 || value[i-1] == ' ' || value[i-1] == '\t') {
				return strings.TrimSpace(value[:i])
			}
		}
	}
	return value
}

func unquoteEnvValue(value string) (string, error) {
	if value == "" {
		return "", nil
	}
	if len(value) < 2 {
		return value, nil
	}

	quote := value[0]
	if quote != '\'' && quote != '"' {
		return value, nil
	}
	if value[len(value)-1] != quote {
		return "", errors.New("unterminated quoted value")
	}

	unquoted := value[1 : len(value)-1]
	if quote == '\'' {
		return unquoted, nil
	}
	replacer := strings.NewReplacer(`\n`, "\n", `\r`, "\r", `\t`, "\t", `\"`, `"`, `\\`, `\`)
	return replacer.Replace(unquoted), nil
}

func (b *bot) poll(ctx context.Context) error {
	offset := 0
	log.Printf("polling Telegram updates")
	for {
		updates, err := b.getUpdates(ctx, offset)
		if err != nil {
			log.Printf("get updates: %v", err)
			time.Sleep(3 * time.Second)
			continue
		}

		for _, upd := range updates {
			offset = upd.UpdateID + 1
			if err := b.handleMessage(ctx, upd.Message); err != nil {
				log.Printf("handle message: %v", err)
			}
		}
	}
}

func (b *bot) handleMessage(ctx context.Context, msg telegramMessage) error {
	if msg.MessageID == 0 || msg.Text == "" || msg.From.IsBot {
		if msg.MessageID != 0 {
			log.Printf("skip message id=%d chat_id=%d: empty text or from bot", msg.MessageID, msg.Chat.ID)
		}
		return nil
	}

	if handled, err := b.handleConfigCommand(ctx, msg); handled {
		return err
	}

	author := displayName(msg.From)
	b.histories.add(msg.Chat.ID, chatMessage{
		Role: "user",
		Text: msg.Text,
	})

	shouldAnswer, prompt, reason := b.shouldAnswer(msg)
	if !shouldAnswer {
		log.Printf("skip message id=%d chat_id=%d type=%s from=%s text=%q decision=%s", msg.MessageID, msg.Chat.ID, msg.Chat.Type, author, logPreview(msg.Text), reason)
		return nil
	}
	log.Printf("answer message id=%d chat_id=%d type=%s from=%s text=%q decision=%s prompt=%q", msg.MessageID, msg.Chat.ID, msg.Chat.Type, author, logPreview(msg.Text), reason, logPreview(prompt))

	switch strings.TrimSpace(prompt) {
	case "/start", "/help":
		log.Printf("send help for message id=%d", msg.MessageID)
		return b.sendMessage(ctx, msg.Chat.ID, "Напишите /ask вопрос, упомяните меня или ответьте на моё сообщение. Настройки: /settings, /set_promt, /set_probability.", msg.MessageID)
	}
	if prompt == "" {
		log.Printf("empty prompt for message id=%d", msg.MessageID)
		return b.sendMessage(ctx, msg.Chat.ID, "Что обсудим?", msg.MessageID)
	}

	if err := b.sendChatAction(ctx, msg.Chat.ID, "typing"); err != nil {
		log.Printf("send chat action: %v", err)
	}

	started := time.Now()
	answer, usedModel, err := b.askLLM(ctx, msg.Chat.ID, prompt)
	if err != nil {
		log.Printf("ask LLM for chat %d message %d: %v", msg.Chat.ID, msg.MessageID, err)
		return nil
	}
	answer = trimTelegramMessage(answer)
	log.Printf("LLM answered message id=%d in=%s requested_model=%s used_model=%s answer_len=%d answer=%q", msg.MessageID, time.Since(started).Round(time.Millisecond), b.cfg.LLMModel, firstNonEmpty(usedModel, "unknown"), len([]rune(answer)), logPreview(answer))
	b.histories.add(msg.Chat.ID, chatMessage{
		Role: "assistant",
		Text: answer,
	})

	if err := b.sendMessage(ctx, msg.Chat.ID, answer, msg.MessageID); err != nil {
		return err
	}
	log.Printf("sent answer: chat_id=%d reply_to=%d text_len=%d", msg.Chat.ID, msg.MessageID, len([]rune(answer)))
	return nil
}

func (b *bot) shouldAnswer(msg telegramMessage) (bool, string, string) {
	text := strings.TrimSpace(msg.Text)
	if strings.HasPrefix(text, "/ask") {
		return true, strings.TrimSpace(strings.TrimPrefix(stripCommandBotSuffix(text, b.me.Username), "/ask")), "command /ask"
	}
	if strings.HasPrefix(text, "/start") || strings.HasPrefix(text, "/help") {
		return true, stripCommandBotSuffix(text, b.me.Username), "help command"
	}

	if msg.Chat.Type == "private" {
		return true, text, "private chat"
	}

	if msg.ReplyToMessage != nil && msg.ReplyToMessage.From.ID == b.me.ID {
		return true, text, "reply to bot"
	}

	mention := "@" + strings.ToLower(b.me.Username)
	if b.me.Username != "" && strings.Contains(strings.ToLower(text), mention) {
		cleaned := strings.TrimSpace(strings.ReplaceAll(text, mention, ""))
		cleaned = strings.TrimSpace(strings.ReplaceAll(cleaned, "@"+b.me.Username, ""))
		return true, cleaned, "bot mention"
	}

	if msg.Chat.Type == "group" || msg.Chat.Type == "supergroup" {
		replyProb := b.replyProb()
		if randomChance(replyProb) {
			return true, text, fmt.Sprintf("random group reply hit probability=%.2f", replyProb)
		}
		return false, "", fmt.Sprintf("random group reply missed probability=%.2f", replyProb)
	}

	return false, "", "unsupported chat type"
}

func (b *bot) handleConfigCommand(ctx context.Context, msg telegramMessage) (bool, error) {
	command, args, ok := splitBotCommand(msg.Text, b.me.Username)
	if !ok {
		return false, nil
	}

	switch command {
	case "/settings":
		log.Printf("send settings for message id=%d", msg.MessageID)
		return true, b.sendPlainMessage(ctx, msg.Chat.ID, b.currentSettingsText(), msg.MessageID)
	case "/set_promt", "/set_prompt":
		if args == "" {
			return true, b.sendMessage(ctx, msg.Chat.ID, "Использование: /set_promt новый системный промт", msg.MessageID)
		}
		b.updateSystemPrompt(args)
		log.Printf("system prompt updated by user=%s chat_id=%d prompt=%q", displayName(msg.From), msg.Chat.ID, logPreview(args))
		return true, b.sendMessage(ctx, msg.Chat.ID, "Системный промт обновлён. История чатов сброшена, чтобы новая инструкция применялась сразу.", msg.MessageID)
	case "/set_probability":
		if args == "" {
			return true, b.sendMessage(ctx, msg.Chat.ID, "Использование: /set_probability 0.5", msg.MessageID)
		}

		probability, err := strconv.ParseFloat(strings.ReplaceAll(args, ",", "."), 64)
		if err != nil || probability < 0 || probability > 1 {
			return true, b.sendMessage(ctx, msg.Chat.ID, "Вероятность должна быть числом от 0 до 1, например /set_probability 0.5", msg.MessageID)
		}

		b.setReplyProb(probability)
		log.Printf("reply probability updated by user=%s chat_id=%d probability=%.2f", displayName(msg.From), msg.Chat.ID, probability)
		return true, b.sendMessage(ctx, msg.Chat.ID, fmt.Sprintf("Вероятность ответа обновлена: %.2f", probability), msg.MessageID)
	default:
		return false, nil
	}
}

func splitBotCommand(text string, botUsername string) (string, string, bool) {
	text = strings.TrimSpace(text)
	if !strings.HasPrefix(text, "/") {
		return "", "", false
	}

	commandToken := text
	args := ""
	if index := strings.IndexFunc(text, unicode.IsSpace); index >= 0 {
		commandToken = text[:index]
		args = strings.TrimSpace(text[index:])
	}

	command, suffix, hasSuffix := strings.Cut(commandToken, "@")
	if hasSuffix {
		if botUsername == "" || !strings.EqualFold(suffix, botUsername) {
			return "", "", false
		}
	}

	return strings.ToLower(command), args, true
}

func (b *bot) systemPrompt() string {
	b.cfgMu.RLock()
	defer b.cfgMu.RUnlock()
	return b.cfg.SystemPrompt
}

func (b *bot) updateSystemPrompt(prompt string) {
	b.cfgMu.Lock()
	b.cfg.SystemPrompt = prompt
	b.cfgMu.Unlock()

	b.histories.clear()
}

func (b *bot) replyProb() float64 {
	b.cfgMu.RLock()
	defer b.cfgMu.RUnlock()
	return b.cfg.ReplyProb
}

func (b *bot) setReplyProb(probability float64) {
	b.cfgMu.Lock()
	defer b.cfgMu.Unlock()
	b.cfg.ReplyProb = probability
}

func (b *bot) currentSettingsText() string {
	b.cfgMu.RLock()
	defer b.cfgMu.RUnlock()

	return fmt.Sprintf("Текущие настройки:\nВероятность ответа: %.2f\nСистемный промт:\n%s", b.cfg.ReplyProb, b.cfg.SystemPrompt)
}

func randomChance(probability float64) bool {
	if probability <= 0 {
		return false
	}
	if probability >= 1 {
		return true
	}

	var raw [8]byte
	if _, err := cryptorand.Read(raw[:]); err != nil {
		return time.Now().UnixNano()%1000000 < int64(probability*1000000)
	}

	n := binary.BigEndian.Uint64(raw[:]) >> 11
	return float64(n)/float64(uint64(1)<<53) < probability
}

func (b *bot) askLLM(ctx context.Context, chatID int64, prompt string) (string, string, error) {
	history := b.histories.get(chatID)
	log.Printf("ask LLM: chat_id=%d history_messages=%d prompt=%q", chatID, len(history), logPreview(prompt))
	messages := []llmMessage{{Role: "system", Content: b.systemPrompt()}}

	for _, item := range history {
		role := item.Role
		if role != "assistant" {
			role = "user"
		}
		messages = append(messages, llmMessage{Role: role, Content: item.Text})
	}
	messages = append(messages, llmMessage{Role: "user", Content: prompt})

	reqBody := llmRequest{
		Model:       b.cfg.LLMModel,
		Messages:    messages,
		Temperature: 0.7,
		MaxTokens:   700,
	}

	var respBody llmResponse
	if err := b.postJSON(ctx, b.cfg.LLMBaseURL+"/chat/completions", reqBody, &respBody, map[string]string{
		"Authorization": "Bearer " + b.cfg.LLMAPIKey,
		"HTTP-Referer":  "https://github.com/local/tg-pint",
		"X-Title":       "tg-pint",
	}); err != nil {
		return "", "", err
	}
	if respBody.Error != nil && respBody.Error.Message != "" {
		return "", respBody.Model, errors.New(respBody.Error.Message)
	}
	if len(respBody.Choices) == 0 {
		return "", respBody.Model, errors.New("empty LLM response")
	}

	answer := strings.TrimSpace(respBody.Choices[0].Message.Content)
	if answer == "" {
		return "", respBody.Model, errors.New("empty LLM message")
	}
	return answer, respBody.Model, nil
}

func (b *bot) getMe(ctx context.Context) (telegramUser, error) {
	var resp telegramResponse[telegramUser]
	err := b.getJSON(ctx, b.telegramURL("getMe"), &resp)
	if err != nil {
		return telegramUser{}, err
	}
	if !resp.OK {
		return telegramUser{}, errors.New(resp.Description)
	}
	return resp.Result, nil
}

func (b *bot) getUpdates(ctx context.Context, offset int) ([]update, error) {
	url := b.telegramURL("getUpdates") + "?timeout=50&allowed_updates=%5B%22message%22%5D"
	if offset > 0 {
		url += "&offset=" + strconv.Itoa(offset)
	}

	var resp telegramResponse[[]update]
	err := b.getJSON(ctx, url, &resp)
	if err != nil {
		return nil, err
	}
	if !resp.OK {
		return nil, errors.New(resp.Description)
	}
	return resp.Result, nil
}

func (b *bot) sendMessage(ctx context.Context, chatID int64, text string, replyTo int) error {
	return b.sendTextMessage(ctx, chatID, markdownToTelegramHTML(text), "HTML", replyTo)
}

func (b *bot) sendPlainMessage(ctx context.Context, chatID int64, text string, replyTo int) error {
	return b.sendTextMessage(ctx, chatID, text, "", replyTo)
}

func (b *bot) sendTextMessage(ctx context.Context, chatID int64, text string, parseMode string, replyTo int) error {
	payload := map[string]any{
		"chat_id": chatID,
		"text":    text,
	}
	if parseMode != "" {
		payload["parse_mode"] = parseMode
	}
	if replyTo > 0 {
		payload["reply_parameters"] = map[string]any{"message_id": replyTo}
	}

	var resp telegramResponse[telegramMessage]
	err := b.postJSON(ctx, b.telegramURL("sendMessage"), payload, &resp, nil)
	if err != nil {
		return err
	}
	if !resp.OK {
		return errors.New(resp.Description)
	}
	return nil
}

func (b *bot) sendChatAction(ctx context.Context, chatID int64, action string) error {
	log.Printf("send chat action: chat_id=%d action=%s", chatID, action)
	payload := map[string]any{
		"chat_id": chatID,
		"action":  action,
	}
	var resp telegramResponse[bool]
	err := b.postJSON(ctx, b.telegramURL("sendChatAction"), payload, &resp, nil)
	if err != nil {
		return err
	}
	if !resp.OK {
		return errors.New(resp.Description)
	}
	return nil
}

func (b *bot) telegramURL(method string) string {
	return "https://api.telegram.org/bot" + b.cfg.TelegramToken + "/" + method
}

func (b *bot) getJSON(ctx context.Context, url string, target any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	return b.doJSON(req, target)
}

func (b *bot) postJSON(ctx context.Context, url string, body any, target any, headers map[string]string) error {
	raw, err := json.Marshal(body)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	return b.doJSON(req, target)
}

func (b *bot) doJSON(req *http.Request, target any) error {
	resp, err := b.http.Do(req)
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := resp.Body.Close(); closeErr != nil {
			log.Printf("close response body: %v", closeErr)
		}
	}()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("%s: %s", resp.Status, strings.TrimSpace(string(raw)))
	}
	if err := json.Unmarshal(raw, target); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	return nil
}

func (h *chatHistories) add(chatID int64, msg chatMessage) {
	h.mu.Lock()
	defer h.mu.Unlock()

	items := append(h.items[chatID], msg)
	if len(items) > maxHistory {
		items = items[len(items)-maxHistory:]
	}
	h.items[chatID] = items
}

func (h *chatHistories) get(chatID int64) []chatMessage {
	h.mu.Lock()
	defer h.mu.Unlock()

	items := h.items[chatID]
	result := make([]chatMessage, len(items))
	copy(result, items)
	return result
}

func (h *chatHistories) clear() {
	h.mu.Lock()
	defer h.mu.Unlock()

	h.items = make(map[int64][]chatMessage)
}

func displayName(user telegramUser) string {
	if user.Username != "" {
		return "@" + user.Username
	}
	if user.FirstName != "" {
		return user.FirstName
	}
	return strconv.FormatInt(user.ID, 10)
}

func stripCommandBotSuffix(text string, botUsername string) string {
	if botUsername == "" {
		return text
	}
	return strings.Replace(text, "@"+botUsername, "", 1)
}

func trimTelegramMessage(text string) string {
	text = strings.TrimSpace(text)
	if len([]rune(text)) <= maxMessageLength {
		return text
	}

	runes := []rune(text)
	return strings.TrimSpace(string(runes[:maxMessageLength])) + "\n..."
}

func logPreview(text string) string {
	text = strings.Join(strings.Fields(text), " ")
	runes := []rune(text)
	if len(runes) <= maxLogTextLength {
		return text
	}
	return string(runes[:maxLogTextLength]) + "..."
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
