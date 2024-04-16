package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"strings"

	"time"

	"chatgptbot/pkg/openai"

	"github.com/caarlos0/env/v7"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

var cfg struct {
	TelegramAPIToken                    string  `env:"TELEGRAM_APITOKEN,required"`
	OpenAIAPIKey                        string  `env:"OPENAI_API_KEY,required"`
	ModelTemperature                    float32 `env:"MODEL_TEMPERATURE" envDefault:"1.0"`
	AllowedTelegramID                   []int64 `env:"ALLOWED_TELEGRAM_ID" envSeparator:","`
	ConversationIdleTimeoutSeconds      int     `env:"CONVERSATION_IDLE_TIMEOUT_SECONDS" envDefault:"900"`
	NotifyUserOnConversationIdleTimeout bool    `env:"NOTIFY_USER_ON_CONVERSATION_IDLE_TIMEOUT" envDefault:"false"`
}

type User struct {
	TelegramID     int64
	LastActiveTime time.Time
	HistoryMessage []openai.ChatCompletionMessage
	//	LatestMessage  tgbotapi.Message
}

var users = make(map[int64]*User)

var openAIClient = openai.NewClient(os.Getenv("OPENAI_API_KEY"))

func main() {
	if err := env.Parse(&cfg); err != nil {
		fmt.Printf("%+v\n", err)
		os.Exit(1)
	}

	bot, err := tgbotapi.NewBotAPI(cfg.TelegramAPIToken)
	if err != nil {
		panic(err)
	}

	// bot.Debug = true

	log.Printf("Authorized on account %s", bot.Self.UserName)

	_, _ = bot.Request(tgbotapi.NewSetMyCommands([]tgbotapi.BotCommand{
		{
			Command:     "help",
			Description: "Справка",
		},
		{
			Command:     "new",
			Description: "Очистить контекст",
		},
	}...))

	// check user context expiration every 5 seconds
	go func() {
		for {
			for userID, _ := range users {
				cleared := clearUserContextIfExpires(userID)
				if cleared {
					///lastMessage := user.LatestMessage
					if cfg.NotifyUserOnConversationIdleTimeout {
						//msg := tgbotapi.NewEditMessageText(userID, lastMessage.MessageID, lastMessage.Text+"\n\nContext cleared due to inactivity.")
						//msg := tgbotapi.NewEditMessageText(user., lastMessage.MessageID, "Context cleared due to inactivity.")
						//_, _ = bot.Send(msg)
					}
				}
			}
			time.Sleep(time.Minute)
		}
	}()

	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60

	updates := bot.GetUpdatesChan(u)

	for update := range updates {
		if update.Message == nil { // ignore any non-Message updates
			continue
		}

		_, err := bot.Send(tgbotapi.NewChatAction(update.Message.Chat.ID, tgbotapi.ChatTyping))
		if err != nil {
			// Sending chat action returns bool value, which causes `Send` to return unmarshal error.
			// So we need to check if it's an unmarshal error and ignore it.
			var unmarshalError *json.UnmarshalTypeError
			if !errors.As(err, &unmarshalError) {
				if err != nil {
					log.Printf("Error in sending message: %v", err) // Более подробное логирование ошибок
					continue                                        // Продолжить цикл в случае ошибки
				}
			}
		}

		if len(cfg.AllowedTelegramID) != 0 {
			var userAllowed bool
			for _, allowedID := range cfg.AllowedTelegramID {
				if allowedID == update.Message.Chat.ID {
					userAllowed = true
				}
			}
			if !userAllowed {
				_, err := bot.Send(tgbotapi.NewMessage(update.Message.Chat.ID, fmt.Sprintf("You are not allowed to use this bot. User ID: %d", update.Message.Chat.ID)))
				if err != nil {
					log.Print(err)
				}
				continue
			}
		}

		if update.Message.IsCommand() { // ignore any non-command Messages
			// Create a new MessageConfig. We don't have text yet,
			// so we leave it empty.
			msg := tgbotapi.NewMessage(update.Message.Chat.ID, "")

			// Extract the command from the Message.
			switch update.Message.Command() {
			case "start":
				msg.Text = "Welcome to ChatGPT bot! Write something to start a conversation. Use /new to clear context and start a new conversation."
			case "help":
				msg.Text = "Напиши что-нибудь для начала общения. /new  очистить контекст, \"нарисуй\" для рисования"
			case "new":
				resetUser(update.Message.From.ID)
				msg.Text = "OK, let's start a new conversation."
			default:
				msg.Text = "I don't know that command"
			}

			if _, err := bot.Send(msg); err != nil {
				log.Printf("Error sending command response: %v", err)
			}
		} else {
			msg := update.Message.Text

			handler := handleUserPrompt

			if strings.Index(strings.ToLower(msg), "нарисуй ") == 0 {
				msg = strings.TrimSpace(msg[len("нарисуй"):])
				handler = handleUserDraw
			}

			answerText, contextTrimmed, err := handler(update.Message.From.ID, update.Message.Text)
			if err != nil {
				log.Print(err)

				err = send(bot, tgbotapi.NewMessage(update.Message.Chat.ID, err.Error()))
				if err != nil {
					log.Print(err)
				}
			} else {
				err = send(bot, tgbotapi.NewMessage(update.Message.Chat.ID, answerText))
				if err != nil {
					log.Print(err)
				}

				if contextTrimmed {
					msg := tgbotapi.NewMessage(update.Message.Chat.ID, "Context trimmed.")
					msg.DisableNotification = true
					err = send(bot, msg)
					if err != nil {
						log.Print(err)
					}
				}
			}
		}
	}
}

func send(bot *tgbotapi.BotAPI, c tgbotapi.Chattable) error {
	msg, err := bot.Send(c)
	if err == nil && msg.Chat != nil {
		//users[msg.Chat.ID].LatestMessage = msg
	}

	return err
}

func handleUserPrompt(userID int64, msg string) (string, bool, error) {
	clearUserContextIfExpires(userID)

	if _, ok := users[userID]; !ok {
		users[userID] = &User{
			TelegramID:     userID,
			LastActiveTime: time.Now(),
			HistoryMessage: []openai.ChatCompletionMessage{},
		}
	}

	users[userID].HistoryMessage = append(users[userID].HistoryMessage, openai.ChatCompletionMessage{
		Role:    "user",
		Content: msg,
	})
	users[userID].LastActiveTime = time.Now()

	req := openai.ChatCompletionRequest{
		Model:       openai.GPT3Dot5Turbo,
		Temperature: cfg.ModelTemperature,
		TopP:        1,
		N:           1,
		// PresencePenalty:  0.2,
		// FrequencyPenalty: 0.2,
		Messages: users[userID].HistoryMessage,
	}

	fmt.Println(req)

	resp, err := openAIClient.CreateChatCompletion(context.Background(), req)
	if err != nil {
		log.Print(err)
		users[userID].HistoryMessage = users[userID].HistoryMessage[:len(users[userID].HistoryMessage)-1]
		return "", false, err
	}

	answer := resp.Choices[0].Message

	users[userID].HistoryMessage = append(users[userID].HistoryMessage, answer)

	var contextTrimmed bool
	if resp.Usage.TotalTokens > 3500 {
		users[userID].HistoryMessage = users[userID].HistoryMessage[1:]
		contextTrimmed = true
	}

	return answer.Content, contextTrimmed, nil
}

func clearUserContextIfExpires(userID int64) bool {
	user := users[userID]
	if user != nil &&
		user.LastActiveTime.Add(time.Duration(cfg.ConversationIdleTimeoutSeconds)*time.Second).Before(time.Now()) {
		resetUser(userID)
		return true
	}

	return false
}

func resetUser(userID int64) {
	delete(users, userID)
}
