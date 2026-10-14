package notify

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
)

// Cliente mínimo de la Bot API oficial de Telegram
// (https://core.telegram.org/bots/api): getMe, getChat, getChatMember,
// getUpdates y sendMessage. El token va en la URL: nunca se registra ni se
// devuelve (scrub y sanitizeRemote).

type tgParams struct {
	RetryAfter      int   `json:"retry_after"`
	MigrateToChatID int64 `json:"migrate_to_chat_id"`
}

type tgResponse struct {
	OK          bool            `json:"ok"`
	ErrorCode   int             `json:"error_code"`
	Description string          `json:"description"`
	Result      json.RawMessage `json:"result"`
	Parameters  *tgParams       `json:"parameters"`
}

// TelegramError es una respuesta ok=false de la Bot API con su descripción
// saneada (sin el token) y los parámetros de reintento o migración.
type TelegramError struct {
	Method      string
	Code        int
	Description string
	RetryAfter  time.Duration
	MigrateTo   int64
}

func (e *TelegramError) Error() string {
	return fmt.Sprintf("telegram %s: %d %s", e.Method, e.Code, e.Description)
}

// webhookActive: Telegram no deja usar getUpdates con un webhook configurado.
func (e *TelegramError) webhookActive() bool {
	return e.Code == http.StatusConflict && strings.Contains(strings.ToLower(e.Description), "webhook")
}

type tgUser struct {
	ID                      int64  `json:"id"`
	IsBot                   bool   `json:"is_bot"`
	FirstName               string `json:"first_name"`
	Username                string `json:"username"`
	CanJoinGroups           bool   `json:"can_join_groups"`
	CanReadAllGroupMessages bool   `json:"can_read_all_group_messages"`
}

type tgChat struct {
	ID        int64  `json:"id"`
	Type      string `json:"type"` // private | group | supergroup | channel
	Title     string `json:"title"`
	Username  string `json:"username"`
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name"`
	IsForum   bool   `json:"is_forum"`
}

type tgChatMember struct {
	Status          string `json:"status"` // creator | administrator | member | restricted | left | kicked
	CanPostMessages *bool  `json:"can_post_messages"`
	CanSendMessages *bool  `json:"can_send_messages"`
}

type tgMessage struct {
	Chat    tgChat `json:"chat"`
	Text    string `json:"text"`
	Caption string `json:"caption"`
}

type tgUpdate struct {
	UpdateID     int64      `json:"update_id"`
	Message      *tgMessage `json:"message"`
	ChannelPost  *tgMessage `json:"channel_post"`
	MyChatMember *struct {
		Chat tgChat `json:"chat"`
	} `json:"my_chat_member"`
}

// chat devuelve el chat y el texto de una actualización.
func (u tgUpdate) chat() (tgChat, string, bool) {
	switch {
	case u.Message != nil:
		return u.Message.Chat, u.Message.Text + " " + u.Message.Caption, true
	case u.ChannelPost != nil:
		return u.ChannelPost.Chat, u.ChannelPost.Text + " " + u.ChannelPost.Caption, true
	case u.MyChatMember != nil:
		return u.MyChatMember.Chat, "", true
	}
	return tgChat{}, "", false
}

// ChatCandidate es un chat detectado en las actualizaciones del bot.
type ChatCandidate struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Title    string `json:"title"`
	Username string `json:"username,omitempty"`
}

func candidate(c tgChat) ChatCandidate {
	title := c.Title
	if title == "" {
		title = strings.TrimSpace(c.FirstName + " " + c.LastName)
	}
	return ChatCandidate{ID: strconv.FormatInt(c.ID, 10), Type: c.Type, Title: title, Username: c.Username}
}

var anyBotTokenRe = regexp.MustCompile(`[0-9]{5,}:[A-Za-z0-9_-]{30,}`)

// sanitizeRemote limpia un texto remoto para mostrarlo: sin el token (ni
// nada con su forma), sin caracteres de control y acotado.
func sanitizeRemote(s, secret string) string {
	if secret != "" {
		s = strings.ReplaceAll(s, secret, "***")
	}
	s = anyBotTokenRe.ReplaceAllString(s, "***")
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, s)
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > 200 {
		s = string(r[:200]) + "…"
	}
	return s
}

// tgCall llama a un método de la Bot API (POST JSON) y decodifica result.
func (s *Senders) tgCall(ctx context.Context, token, method string, params, out any) error {
	body, _ := json.Marshal(params)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.telegramURL(token, method), bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("telegram %s: %w", method, scrub(err, token))
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.client(true, 0).Do(req)
	if err != nil {
		return fmt.Errorf("telegram %s: %w", method, scrub(err, token))
	}
	defer func() { _ = resp.Body.Close() }()
	var r tgResponse
	if derr := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&r); derr != nil {
		if resp.StatusCode != http.StatusOK {
			return &TelegramError{Method: method, Code: resp.StatusCode, Description: "HTTP " + strconv.Itoa(resp.StatusCode)}
		}
		return fmt.Errorf("telegram %s: respuesta no válida", method)
	}
	if !r.OK {
		te := &TelegramError{Method: method, Code: r.ErrorCode, Description: sanitizeRemote(r.Description, token)}
		if te.Code == 0 {
			te.Code = resp.StatusCode
		}
		if r.Parameters != nil {
			te.RetryAfter = time.Duration(r.Parameters.RetryAfter) * time.Second
			te.MigrateTo = r.Parameters.MigrateToChatID
		}
		return te
	}
	if out != nil && len(r.Result) > 0 {
		if err := json.Unmarshal(r.Result, out); err != nil {
			return fmt.Errorf("telegram %s: result: %w", method, err)
		}
	}
	return nil
}

func (s *Senders) getMe(ctx context.Context, token string) (tgUser, error) {
	var u tgUser
	err := s.tgCall(ctx, token, "getMe", map[string]any{}, &u)
	return u, err
}

// chatIDParam: Telegram acepta el id numérico o @usuario_del_canal.
func chatIDParam(id string) any {
	if n, err := strconv.ParseInt(id, 10, 64); err == nil {
		return n
	}
	return id
}

func tgFail(code string, err error, prefix string) TestResult {
	var te *TelegramError
	if errors.As(err, &te) {
		return fail(code, prefix+" Telegram: "+te.Description)
	}
	return classify(err)
}

// testTelegram comprueba sin enviar nada: el token (getMe), que el chat
// existe (getChat) y que el bot es miembro (getChatMember); en un canal,
// además, que es administrador con permiso para publicar.
func (s *Senders) testTelegram(ctx context.Context, cfg TelegramConfig, cr Credentials) TestResult {
	tok := s.telegramToken(cr)
	if tok == "" {
		return fail("missing_credentials", "Sin token de bot (propio o de la instalación).")
	}
	me, err := s.getMe(ctx, tok)
	if err != nil {
		var te *TelegramError
		if errors.As(err, &te) && (te.Code == http.StatusUnauthorized || te.Code == http.StatusNotFound) {
			return fail("auth_failed", "Telegram rechazó el token del bot: "+te.Description)
		}
		return tgFail("unexpected_response", err, "getMe falló.")
	}
	res := TestResult{OK: true}
	if me.Username != "" {
		u := "@" + me.Username
		res.BotUsername = &u
	}
	if cfg.ChatID == "" {
		return res
	}
	var chat tgChat
	if err := s.tgCall(ctx, tok, "getChat", map[string]any{"chat_id": chatIDParam(cfg.ChatID)}, &chat); err != nil {
		var te *TelegramError
		if errors.As(err, &te) && te.Code == http.StatusForbidden {
			r := fail("bot_not_member", "El bot no está en el chat o fue expulsado. Telegram: "+te.Description)
			r.BotUsername = res.BotUsername
			return r
		}
		r := tgFail("chat_not_found", err, "No se encontró el chat: añada el bot al grupo o canal (o escríbale /start) y revise el chat_id.")
		r.BotUsername = res.BotUsername
		return r
	}
	title := candidate(chat).Title
	res.ChatTitle = &title
	if chat.Type == "private" {
		return res
	}
	var member tgChatMember
	if err := s.tgCall(ctx, tok, "getChatMember", map[string]any{"chat_id": chatIDParam(cfg.ChatID), "user_id": me.ID}, &member); err != nil {
		r := tgFail("bot_not_member", err, "No se pudo comprobar que el bot sea miembro del chat.")
		r.BotUsername, r.ChatTitle = res.BotUsername, res.ChatTitle
		return r
	}
	bad := TestResult{}
	switch {
	case member.Status == "left" || member.Status == "kicked":
		bad = fail("bot_not_member", "El bot no es miembro del chat: añádalo al grupo o canal.")
	case chat.Type == "channel" && member.Status != "administrator" && member.Status != "creator":
		bad = fail("bot_cannot_post", "En un canal el bot debe ser administrador con permiso para publicar mensajes.")
	case chat.Type == "channel" && member.Status == "administrator" && (member.CanPostMessages == nil || !*member.CanPostMessages):
		bad = fail("bot_cannot_post", "El bot es administrador del canal pero sin el permiso «Publicar mensajes».")
	case member.Status == "restricted" && member.CanSendMessages != nil && !*member.CanSendMessages:
		bad = fail("bot_cannot_post", "El bot está restringido y no puede enviar mensajes en el grupo.")
	case cfg.MessageThreadID != nil && !chat.IsForum:
		bad = fail("unexpected_response", "El chat no tiene temas: quite message_thread_id o active los temas del grupo.")
	}
	if bad.ErrorCode != nil {
		bad.BotUsername, bad.ChatTitle = res.BotUsername, res.ChatTitle
		return bad
	}
	return res
}

var severityEmoji = map[string]string{"critical": "🔴", "high": "🟠", "medium": "🟡", "low": "🔵", "info": "🟢"}

var (
	htmlText = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")
	htmlAttr = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;")
)

// telegramMaxText es el límite de sendMessage (4096 caracteres tras el parseo).
const telegramMaxText = 4000

// formatTelegram arma el mensaje en HTML de Telegram: todo texto dinámico
// escapado (<, >, &), emoji de severidad y el enlace como <a href>.
func formatTelegram(m Message, severity string) string {
	text := m.Text
	if r := []rune(text); len(r) > telegramMaxText-400 {
		text = string(r[:telegramMaxText-400]) + "…"
	}
	var b strings.Builder
	if e := severityEmoji[severity]; e != "" {
		b.WriteString(e + " ")
	}
	b.WriteString("<b>" + htmlText.Replace(m.Subject) + "</b>")
	if text != "" {
		b.WriteString("\n" + htmlText.Replace(text))
	}
	if strings.HasPrefix(m.Link, "https://") || strings.HasPrefix(m.Link, "http://") {
		b.WriteString("\n\n<a href=\"" + htmlAttr.Replace(m.Link) + "\">Abrir en Horus</a>")
	}
	return b.String()
}

// sendTelegram envía el mensaje (HTML, sin vista previa de enlaces, al tema
// si hay message_thread_id). Devuelve *TelegramError con retry_after o
// migrate_to_chat_id cuando Telegram los informa.
func (s *Senders) sendTelegram(ctx context.Context, cfg TelegramConfig, cr Credentials, m Message, severity string) error {
	tok := s.telegramToken(cr)
	if tok == "" {
		return errors.New("telegram: no bot token")
	}
	params := map[string]any{"chat_id": chatIDParam(cfg.ChatID), "text": formatTelegram(m, severity), "parse_mode": "HTML",
		"link_preview_options": map[string]any{"is_disabled": true}}
	if cfg.MessageThreadID != nil {
		params["message_thread_id"] = *cfg.MessageThreadID
	}
	return s.tgCall(ctx, tok, "sendMessage", params, nil)
}

// pollLocks serializa getUpdates por bot: dos lecturas simultáneas del mismo
// bot se cancelan entre sí (409) y el offset confirmaría lo del otro.
var pollLocks sync.Map

func pollLock(token string) *sync.Mutex {
	k := sha256.Sum256([]byte(token))
	v, _ := pollLocks.LoadOrStore(k, &sync.Mutex{})
	return v.(*sync.Mutex) //nolint:forcetypeassert // solo guardamos *sync.Mutex
}

// pollUpdates lee las actualizaciones pendientes del bot (sin espera larga),
// se las pasa a handle y las confirma con offset para que no se acumulen.
// Con un webhook configurado Telegram responde 409 (webhookActive).
func (s *Senders) pollUpdates(ctx context.Context, token string, handle func([]tgUpdate) error) error {
	mu := pollLock(token)
	mu.Lock()
	defer mu.Unlock()
	allowed := []string{"message", "channel_post", "my_chat_member"}
	var offset int64
	for page := 0; page < 5; page++ {
		params := map[string]any{"limit": 100, "timeout": 0, "allowed_updates": allowed}
		if offset > 0 {
			params["offset"] = offset
		}
		var ups []tgUpdate
		if err := s.tgCall(ctx, token, "getUpdates", params, &ups); err != nil {
			return err
		}
		if len(ups) == 0 {
			break
		}
		if err := handle(ups); err != nil {
			return err
		}
		for _, u := range ups {
			offset = max(offset, u.UpdateID+1)
		}
		if len(ups) < 100 {
			break
		}
	}
	if offset > 0 {
		// Confirmación: un getUpdates con offset mayor marca las anteriores
		// como leídas (lo que devuelva queda pendiente para la próxima).
		if err := s.tgCall(ctx, token, "getUpdates", map[string]any{"offset": offset, "limit": 1, "timeout": 0,
			"allowed_updates": allowed}, nil); err != nil {
			return err
		}
	}
	return nil
}
