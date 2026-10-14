package notify

import (
	"context"
	"crypto/rand"
	"errors"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/hcdestroyer/horus-flow/packages/go/apperr"
)

// Códigos de error de los asistentes de Telegram.
const (
	CodeTelegramWebhookActive    = "NOTIFICATION_TELEGRAM_WEBHOOK_ACTIVE"
	CodeTelegramLinkCodeInvalid  = "NOTIFICATION_TELEGRAM_LINK_CODE_INVALID"
	CodeTelegramBotNotConfigured = "NOTIFICATION_TELEGRAM_BOT_NOT_CONFIGURED"
	linkCodeTTL                  = 15 * time.Minute
	linkCodeAlphabet             = "abcdefghijkmnpqrstuvwxyz23456789"
)

var linkCodeRe = regexp.MustCompile(`^hf[a-z0-9]{10}$`)

// BotInfo es lo que se muestra de un bot validado (getMe).
type BotInfo struct {
	ID                      int64  `json:"id"`
	Username                string `json:"username"`
	FirstName               string `json:"first_name"`
	CanJoinGroups           bool   `json:"can_join_groups"`
	CanReadAllGroupMessages bool   `json:"can_read_all_group_messages"`
}

// telegramErr traduce un error de la Bot API a un problema del contrato
// (con la descripción de Telegram saneada, nunca el token).
func telegramErr(err error) error {
	var te *TelegramError
	if errors.As(err, &te) {
		switch {
		case te.webhookActive():
			return apperr.New(apperr.KindInvalid, CodeTelegramWebhookActive,
				"El bot tiene un webhook configurado y Telegram no permite leer sus mensajes con getUpdates: elimínelo (deleteWebhook) o use otro bot. Telegram: "+te.Description)
		case te.Code == http.StatusUnauthorized || te.Code == http.StatusNotFound:
			return apperr.New(apperr.KindInvalid, CodeChannelAuthFailed, "Telegram rechazó el token del bot: "+te.Description)
		case te.Code == http.StatusConflict:
			return apperr.New(apperr.KindConflict, CodeChannelUnreachable,
				"Otro proceso está leyendo los mensajes de este bot; inténtelo de nuevo en unos segundos. Telegram: "+te.Description)
		case te.Code == http.StatusTooManyRequests:
			return &apperr.Error{Kind: apperr.KindTooManyRequests, Code: CodeChannelUnreachable, Detail: "Telegram limita las peticiones: " + te.Description,
				RetryAfter: int(te.RetryAfter.Seconds())}
		}
		return apperr.New(apperr.KindBadGateway, CodeChannelUnreachable, "Telegram: "+te.Description)
	}
	return apperr.New(apperr.KindBadGateway, CodeChannelUnreachable, "No se pudo contactar con Telegram.")
}

func validBotToken(token string) error {
	if !botTokenRe.MatchString(token) {
		return apperr.Validation(apperr.Field("bot_token", "INVALID_FORMAT",
			"Token de bot no válido: es el texto que da @BotFather tras /newbot (número:letras)."))
	}
	return nil
}

// ValidateTelegramBot es POST /notification-channels/telegram/bot-validation:
// getMe con un token pegado en el asistente (no se guarda).
func (s *Service) ValidateTelegramBot(ctx context.Context, token string) (BotInfo, error) {
	if _, _, err := scope(ctx, PermManage); err != nil {
		return BotInfo{}, err
	}
	token = strings.TrimSpace(token)
	if err := validBotToken(token); err != nil {
		return BotInfo{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	u, err := s.senders.getMe(ctx, token)
	if err != nil {
		return BotInfo{}, telegramErr(err)
	}
	return BotInfo{ID: u.ID, Username: u.Username, FirstName: u.FirstName, CanJoinGroups: u.CanJoinGroups,
		CanReadAllGroupMessages: u.CanReadAllGroupMessages}, nil
}

// LinkCodeResult es la respuesta de POST …/telegram/link-codes.
type LinkCodeResult struct {
	Code        string
	ExpiresAt   time.Time
	BotUsername *string
	StartURL    *string
}

func newLinkCode() string {
	b := make([]byte, 10)
	_, _ = rand.Read(b)
	out := make([]byte, 10)
	for i, v := range b {
		out[i] = linkCodeAlphabet[int(v)%len(linkCodeAlphabet)]
	}
	return "hf" + string(out)
}

// CreateTelegramLinkCode genera un código de un solo uso (15 min) para
// detectar el chat con el bot compartido de la instalación.
func (s *Service) CreateTelegramLinkCode(ctx context.Context) (LinkCodeResult, error) {
	_, t, err := scope(ctx, PermManage)
	if err != nil {
		return LinkCodeResult{}, err
	}
	if s.senders.TelegramToken == "" {
		return LinkCodeResult{}, apperr.New(apperr.KindInvalid, CodeTelegramBotNotConfigured,
			"La instalación no tiene bot de Telegram (HORUS_TELEGRAM_BOT_TOKEN_FILE): cree un bot propio con @BotFather.")
	}
	now := s.ts()
	lc := &LinkCode{Code: newLinkCode(), TenantID: t.UUID(), CreatedAt: now, ExpiresAt: now.Add(linkCodeTTL)}
	if err := s.st.insertLinkCode(ctx, t, lc); err != nil {
		return LinkCodeResult{}, err
	}
	res := LinkCodeResult{Code: lc.Code, ExpiresAt: lc.ExpiresAt}
	mctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if u, err := s.senders.getMe(mctx, s.senders.TelegramToken); err == nil && u.Username != "" {
		name := "@" + u.Username
		start := "https://t.me/" + u.Username + "?start=" + lc.Code
		res.BotUsername, res.StartURL = &name, &start
	}
	return res, nil
}

// DetectInput es el cuerpo de POST …/telegram/chat-detection.
type DetectInput struct {
	BotToken  string     `json:"bot_token"`
	ChannelID *uuid.UUID `json:"channel_id"`
	Code      string     `json:"code"`
}

// DetectResult son los chats candidatos.
type DetectResult struct {
	Chats       []ChatCandidate
	BotUsername *string
	SharedBot   bool
}

var codeTokenRe = regexp.MustCompile(`hf[a-z0-9]{10}`)

// DetectTelegramChats lee las actualizaciones pendientes del bot (getUpdates,
// sin webhook) y devuelve los chats donde escribieron o lo añadieron. Con un
// bot propio (token pegado o guardado en el canal) devuelve todos; con el bot
// compartido de la instalación solo los que enviaron el código del ISP,
// porque sus actualizaciones mezclan a todos los ISP.
func (s *Service) DetectTelegramChats(ctx context.Context, in DetectInput) (DetectResult, error) {
	_, t, err := scope(ctx, PermManage)
	if err != nil {
		return DetectResult{}, err
	}
	token := strings.TrimSpace(in.BotToken)
	if token != "" {
		if err := validBotToken(token); err != nil {
			return DetectResult{}, err
		}
	} else if in.ChannelID != nil {
		_, c, err := s.get(ctx, PermManage, *in.ChannelID)
		if err != nil {
			return DetectResult{}, err
		}
		if c.Kind != KindTelegram {
			return DetectResult{}, apperr.Validation(apperr.Field("channel_id", "INVALID_VALUE", "El canal no es de Telegram."))
		}
		cr, err := s.credentials(c)
		if err != nil {
			return DetectResult{}, err
		}
		token = cr.TelegramBotToken
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	if token != "" {
		return s.detectOwnBot(ctx, token)
	}
	// Bot compartido de la instalación: código obligatorio.
	if s.senders.TelegramToken == "" {
		return DetectResult{}, apperr.New(apperr.KindInvalid, CodeTelegramBotNotConfigured,
			"La instalación no tiene bot de Telegram: pegue el token de un bot propio.")
	}
	code := strings.ToLower(strings.TrimSpace(in.Code))
	if !linkCodeRe.MatchString(code) {
		return DetectResult{}, apperr.Validation(apperr.Field("code", "REQUIRED", "Genere un código y envíelo al bot antes de detectar el chat."))
	}
	if _, err := s.st.getLinkCode(ctx, t, code); err != nil {
		if errors.Is(err, errNotFound) {
			return DetectResult{}, apperr.New(apperr.KindInvalid, CodeTelegramLinkCodeInvalid, "El código no existe o caducó: genere otro.")
		}
		return DetectResult{}, err
	}
	err = s.senders.pollUpdates(ctx, s.senders.TelegramToken, func(ups []tgUpdate) error {
		found := map[string]ChatCandidate{}
		for _, u := range ups {
			chat, text, ok := u.chat()
			if !ok || text == "" {
				continue
			}
			for _, c := range codeTokenRe.FindAllString(strings.ToLower(text), 5) {
				found[c] = candidate(chat)
			}
		}
		return s.st.matchLinkCodes(ctx, found)
	})
	if err != nil {
		return DetectResult{}, telegramErr(err)
	}
	lc, err := s.st.getLinkCode(ctx, t, code)
	if err != nil {
		if errors.Is(err, errNotFound) {
			return DetectResult{}, apperr.New(apperr.KindInvalid, CodeTelegramLinkCodeInvalid, "El código caducó: genere otro.")
		}
		return DetectResult{}, err
	}
	res := DetectResult{Chats: []ChatCandidate{}, SharedBot: true}
	if lc.Chat != nil {
		res.Chats = append(res.Chats, *lc.Chat)
	}
	return res, nil
}

func (s *Service) detectOwnBot(ctx context.Context, token string) (DetectResult, error) {
	me, err := s.senders.getMe(ctx, token)
	if err != nil {
		return DetectResult{}, telegramErr(err)
	}
	res := DetectResult{Chats: []ChatCandidate{}}
	if me.Username != "" {
		u := "@" + me.Username
		res.BotUsername = &u
	}
	seen := map[string]int{}
	err = s.senders.pollUpdates(ctx, token, func(ups []tgUpdate) error {
		for _, u := range ups {
			chat, _, ok := u.chat()
			if !ok || chat.ID == 0 {
				continue
			}
			c := candidate(chat)
			if i, dup := seen[c.ID]; dup {
				res.Chats[i] = c // el más reciente (p. ej. tras renombrar)
				continue
			}
			seen[c.ID] = len(res.Chats)
			res.Chats = append(res.Chats, c)
		}
		return nil
	})
	if err != nil {
		return DetectResult{}, telegramErr(err)
	}
	// Canales y grupos primero (lo habitual para un NOC).
	slices.SortStableFunc(res.Chats, func(a, b ChatCandidate) int {
		rank := func(t string) int {
			if t == "private" {
				return 1
			}
			return 0
		}
		return rank(a.Type) - rank(b.Type)
	})
	return res, nil
}
