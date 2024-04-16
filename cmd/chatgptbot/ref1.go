package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"
	"sync"
	"time"

	"github.com/caarlos0/env/v7"
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/MasterDimmy/zipologger"
	"chatgptbot/pkg/openai"
)

type Config struct {
	TelegramAPIToken                    string   `env:"TELEGRAM_APITOKEN,required"`
	OpenAIAPIKey                        string   `env:"OPENAI_API_KEY,required"`
	ModelTemperature                    float32  `env:"MODEL_TEMPERATURE" envDefault:"1.0"`
	ConversationIdleTimeoutSeconds      int      `env:"CONVERSATION_IDLE_TIMEOUT_SECONDS" envDefault:"900"`
	NotifyUserOnConversationIdleTimeout bool     `env:"NOTIFY_USER_ON_CONVERSATION_IDLE_TIMEOUT" envDefault:"false"`
	AdminTelegramIDs                    []int64  `json:"admin_telegram_ids"`
	AllowedTelegramIDs                  []int64  `json:"allowed_telegram_ids"`
}

type User struct {
	TelegramID     int64
	LastActiveTime time.Time
	HistoryMessage []openai.ChatCompletionMessage
}

var (
	cfg    Config
	users  = make(map[int64]*User)
	mutex  sync.Mutex     //protect users
	bot    *tgbotapi.BotAPI
	logger *zipologger.Logger
)

func main() {
	loadConfig()
	setupBot()
	setupCommands()
	monitorUserActivity()
	handleUpdates()
}

func loadConfig() {
	if err := env.Parse(&cfg); err != nil {
		fmt.Printf("error loading env variables: %+v\n", err)
		os.Exit(1)
	}

	data, err := ioutil.ReadFile("config.cfg")
	if err != nil {
		fmt.Printf("error reading config file: %s\n", err)
		return
	}
	if err = json.Unmarshal(data, &cfg); err != nil {
		fmt.Printf("error unmarshalling config file: %s\n", err)
		return
	}

	logger = zipologger.NewLogger("./logs/actions.log", 5, 5, 5, false)
}

func setupBot() {
	var err error
	bot, err = tgbotapi.NewBotAPI(cfg.TelegramAPIToken)
	if err != nil {
		logger.Printf("error creating bot: %s\n", err)
		os.Exit(1)
	}

	logger.Printf("Authorized on account %s\n", bot.Self.UserName)
}

func setupCommands() {
	commands := []tgbotapi.BotCommand{
		{Command: "help", Description: "Help"},
		{Command: "new", Description: "Clear context"},
		{Command: "listusers", Description: "List allowed users (only admin)"},
		{Command: "adduser", Description: "Add user (only admin)"},
		{Command: "removeuser", Description: "Remove user (only admin)"},
	}
	if _, err := bot.SetMyCommands(commands); err != nil {
		logger.Printf("error setting commands: %s\n", err)
	}
}

func monitorUserActivity() {
	go func() {
		for {
			time.Sleep(time.Minute)
			clearExpiredUserContexts()
		}
	}()
}

func handleUpdates() {
	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60
	updates, err := bot.GetUpdatesChan(u)
	if err != nil {
		logger.Printf("error fetching updates: %s\n", err)
		return
	}

	for update := range updates {
		if update.Message == nil {
			continue
		}
		handleMessage(update)
	}
}

func handleTextMessage(update tgbotapi.Update) {
	mutex.Lock()
	user, exists := users[update.Message.From.ID]
	if !exists {
		user = &User{
			TelegramID:     update.Message.From.ID,
			LastActiveTime: time.Now(),
			HistoryMessage: []openai.ChatCompletionMessage{},
		}
		users[update.Message.From.ID] = user
	}
	mutex.Unlock()

	// Here, you'd normally process the text and possibly call OpenAI's GPT model.
	responseText := "Processed your message: " + update.Message.Text
	msg := tgbotapi.NewMessage(update.Message.Chat.ID, responseText)

	if _, err := bot.Send(msg); err != nil {
		logger.Printf("Error sending text message response: %v", err)
	}
}


func handleMessage(update tgbotapi.Update) {
	userID := update.Message.From.ID
	mutex.Lock()
	defer mutex.Unlock()

	if !isUserAllowed(userID) {
		msg := tgbotapi.NewMessage(update.Message.Chat.ID, "You are not allowed to use this bot.")
		bot.Send(msg)
		return
	}

	if update.Message.IsCommand() {
		handleCommand(update)
	} else {
		handleTextMessage(update)
	}
}


func handleCommand(update tgbotapi.Update) {
	msg := tgbotapi.NewMessage(update.Message.Chat.ID, "")

	switch update.Message.Command() {
	case "start":
		msg.Text = "Welcome to ChatGPT bot! Write something to start a conversation. Use /new to clear context and start a new conversation."
	case "help":
		msg.Text = "Напиши что-нибудь для начала общения. /new очистить контекст, \"нарисуй\" для рисования"
	case "listusers":
		if !isAdmin(update.Message.From.ID) {
			msg.Text = "action not allowed"
		} else {
			msg.Text = "Connected users:\n"
			mutex.Lock()
			for id, user := range users {
				msg.Text += fmt.Sprintf("%d - %s\n", id, user.TelegramID)
			}
			mutex.Unlock()
		}
	case "adduser", "removeuser":
		if !isAdmin(update.Message.From.ID) {
			msg.Text = "action not allowed"
		} else {
			handleUserManagement(update)
		}
	case "new":
		resetUser(update.Message.From.ID)
		msg.Text = "OK, let's start a new conversation."
	default:
		msg.Text = "I don't know that command"
	}

	if _, err := bot.Send(msg); err != nil {
		logger.Printf("Error sending command response: %v", err)
	}
}

func isAdmin(userID int64) bool {
	for _, id := range cfg.AdminTelegramIDs {
		if userID == id {
			return true
		}
	}
	return false
}

func handleUserManagement(update tgbotapi.Update) {
	args := strings.Split(update.Message.CommandArguments(), " ")
	msg := tgbotapi.NewMessage(update.Message.Chat.ID, "")

	if len(args) < 1 {
		msg.Text = "Provide user ID."
		bot.Send(msg)
		return
	}

	userID, err := strconv.ParseInt(args[0], 10, 64)
	if err != nil {
		msg.Text = fmt.Sprintf("Invalid user ID: %s", args[0])
		bot.Send(msg)
		return
	}

	if update.Message.Command() == "adduser" {
		cfg.AllowedTelegramIDs = append(cfg.AllowedTelegramIDs, userID)
		msg.Text = fmt.Sprintf("User ID %d added successfully.", userID)
	} else {
		for i, id := range cfg.AllowedTelegramIDs {
			if id == userID {
				cfg.AllowedTelegramIDs = append(cfg.AllowedTelegramIDs[:i], cfg.AllowedTelegramIDs[i+1:]...)
				msg.Text = fmt.Sprintf("User ID %d removed successfully.", userID)
				break
			}
		}
		if msg.Text == "" {
			msg.Text = fmt.Sprintf("User ID %d not found.", userID)
		}
	}

	if _, err := json.Marshal(&cfg); err != nil {
		logger.Printf("Error saving config: %s", err)
		return
	}

	if _, err := bot.Send(msg); err != nil {
		logger.Printf("Error sending user management response: %v", err)
	}
}


func isUserAllowed(userID int64) bool {
	for _, id := range cfg.AllowedTelegramIDs {
		if id == userID {
			return true
		}
	}
	return false
}

func clearExpiredUserContexts() {
	mutex.Lock()
	for id, user := range users {
		if time.Since(user.LastActiveTime) > time.Duration(cfg.ConversationIdleTimeoutSeconds)*time.Second {
			delete(users, id)
			if cfg.NotifyUserOnConversationIdleTimeout {
				msg := tgbotapi.NewMessage(id, "Context cleared due to inactivity.")
				bot.Send(msg)
			}
		}
	}
	mutex.Unlock()
}



