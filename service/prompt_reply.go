package service

import (
	"fmt"
	"log"
	"strings"

	"github.com/woodchen-ink/Q58Bot/core"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

var db *core.Database

func InitPromptService() error {
	var err error
	db, err = core.NewDatabase()
	if err != nil {
		return fmt.Errorf("failed to initialize database: %v", err)
	}
	return nil
}

func SetPromptReply(prompt, reply string) error {
	return db.AddPromptReply(prompt, reply)
}

func DeletePromptReply(prompt string) error {
	return db.DeletePromptReply(prompt)
}

func GetPromptReply(message string) (string, bool) {
	promptReplies, err := db.GetAllPromptReplies()
	if err != nil {
		log.Printf("Error getting prompt replies: %v", err)
		return "", false
	}

	for prompt, reply := range promptReplies {
		if strings.Contains(strings.ToLower(message), prompt) {
			return reply, true
		}
	}
	return "", false
}

func ListPromptReplies() string {
	promptReplies, err := db.GetAllPromptReplies()
	if err != nil {
		log.Printf("Error getting prompt replies: %v", err)
		return "获取提示词回复时发生错误。"
	}

	if len(promptReplies) == 0 {
		return "目前没有设置任何提示词回复。"
	}

	var result strings.Builder
	result.WriteString("当前设置的提示词回复：\n")
	for prompt, reply := range promptReplies {
		result.WriteString(fmt.Sprintf("提示词: %s\n回复: %s\n\n", prompt, reply))
	}
	return result.String()
}

func HandlePromptCommand(bot *tgbotapi.BotAPI, message *tgbotapi.Message) {
	if !core.IsAdmin(message.From.ID) {
		bot.Send(tgbotapi.NewMessage(message.Chat.ID, "只有管理员才能使用此命令。"))
		return
	}

	args := strings.SplitN(message.Text, " ", 3)
	if len(args) < 2 {
		bot.Send(tgbotapi.NewMessage(message.Chat.ID, "使用方法: /prompt set <提示词> <回复>\n/prompt delete <提示词>\n/prompt list"))
		return
	}

	switch args[1] {
	case "set":
		if len(args) < 3 {
			bot.Send(tgbotapi.NewMessage(message.Chat.ID, "使用方法: /prompt set <提示词> <回复>"))
			return
		}
		promptAndReply := strings.SplitN(args[2], " ", 2)
		if len(promptAndReply) < 2 {
			bot.Send(tgbotapi.NewMessage(message.Chat.ID, "请同时提供提示词和回复。"))
			return
		}
		err := SetPromptReply(promptAndReply[0], promptAndReply[1])
		if err != nil {
			bot.Send(tgbotapi.NewMessage(message.Chat.ID, fmt.Sprintf("设置提示词失败：%v", err)))
			return
		}
		bot.Send(tgbotapi.NewMessage(message.Chat.ID, fmt.Sprintf("已设置提示词 '%s' 的回复。", promptAndReply[0])))
	case "delete":
		if len(args) < 3 {
			bot.Send(tgbotapi.NewMessage(message.Chat.ID, "使用方法: /prompt delete <提示词>"))
			return
		}
		err := DeletePromptReply(args[2])
		if err != nil {
			bot.Send(tgbotapi.NewMessage(message.Chat.ID, fmt.Sprintf("删除提示词失败：%v", err)))
			return
		}
		bot.Send(tgbotapi.NewMessage(message.Chat.ID, fmt.Sprintf("已删除提示词 '%s' 的回复。", args[2])))
	case "list":
		bot.Send(tgbotapi.NewMessage(message.Chat.ID, ListPromptReplies()))
	default:
		bot.Send(tgbotapi.NewMessage(message.Chat.ID, "未知的子命令。使用方法: /prompt set|delete|list"))
	}
}

func CheckAndReplyPrompt(bot *tgbotapi.BotAPI, message *tgbotapi.Message) {
	if reply, found := GetPromptReply(message.Text); found {
		replyMsg := tgbotapi.NewMessage(message.Chat.ID, reply)
		replyMsg.ReplyToMessageID = message.MessageID
		bot.Send(replyMsg)
	}
}