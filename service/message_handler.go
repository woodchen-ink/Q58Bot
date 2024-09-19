// 消息处理函数
package service

import (
	"fmt"
	"log"
	"strings"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/woodchen-ink/Q58Bot/core"
	"github.com/woodchen-ink/Q58Bot/service/group_member_management"
	"github.com/woodchen-ink/Q58Bot/service/link_filter"
	"github.com/woodchen-ink/Q58Bot/service/prompt_reply"
)

// handleUpdate 处理所有传入的更新信息，包括消息和命令, 然后分开处理。
func handleUpdate(bot *tgbotapi.BotAPI, update tgbotapi.Update, linkFilter *link_filter.LinkFilter, rateLimiter *core.RateLimiter, db *core.Database) {
	// 检查更新是否包含消息，如果不包含则直接返回。
	if update.Message == nil {
		return
	}

	// 如果消息来自私聊且发送者是预定义的管理员，调用处理管理员命令的函数。
	if update.Message.Chat.Type == "private" && update.Message.From.ID == core.ADMIN_ID {
		handleAdminCommand(bot, update.Message, db)
		return
	}

	// 如果消息来自群聊且通过了速率限制器的检查，调用处理普通消息的函数。
	if update.Message.Chat.Type != "private" && rateLimiter.Allow() {
		processMessage(bot, update.Message, linkFilter)
	}
}

// 处理管理员私聊消息
func handleAdminCommand(bot *tgbotapi.BotAPI, message *tgbotapi.Message, db *core.Database) {
	command := message.Command()
	args := message.CommandArguments()

	switch command {
	case "add", "delete", "list", "deletecontaining":
		HandleKeywordCommand(bot, message, command, args, db)
	case "addwhite", "delwhite", "listwhite":
		HandleWhitelistCommand(bot, message, command, args, db)
	case "prompt":
		prompt_reply.HandlePromptCommand(bot, message)
	default:
		bot.Send(tgbotapi.NewMessage(message.Chat.ID, "未知命令, 听不懂"))
	}
}

// processMessage 处理群里接收到的消息。
func processMessage(bot *tgbotapi.BotAPI, message *tgbotapi.Message, linkFilter *link_filter.LinkFilter) {
	// 记录消息内容
	log.Printf("Processing message: %s", message.Text)

	// 处理 /ban 命令
	if message.ReplyToMessage != nil && message.Text == "/ban" {
		group_member_management.HandleBanCommand(bot, message)
		return
	}

	// 如果不是管理员，才进行链接过滤
	if !core.IsAdmin(message.From.ID) {
		// 判断消息是否应当被过滤及找出新的非白名单链接
		shouldFilter, newLinks := linkFilter.ShouldFilter(message.Text)
		if shouldFilter {
			// 记录被过滤的消息
			log.Printf("消息应该被过滤: %s", message.Text)
			// 删除原始消息
			deleteMsg := tgbotapi.NewDeleteMessage(message.Chat.ID, message.MessageID)
			_, err := bot.Request(deleteMsg)
			if err != nil {
				// 删除消息失败时记录错误
				log.Printf("删除消息失败: %v", err)
			}

			// 发送提示消息
			notification := tgbotapi.NewMessage(message.Chat.ID, "已撤回该消息。注:一个链接不能发两次.")
			sent, err := bot.Send(notification)
			if err != nil {
				// 发送通知失败时记录错误
				log.Printf("发送通知失败: %v", err)
			} else {
				// 3分钟后删除提示消息
				go deleteMessageAfterDelay(bot, message.Chat.ID, sent.MessageID, 3*time.Minute)
			}
			// 结束处理
			return
		}
		// 如果发现新的非白名单链接
		if len(newLinks) > 0 {
			// 记录新的非白名单链接
			log.Printf("发现新的非白名单链接: %v", newLinks)
		}
	}

	// 检查消息文本是否匹配预设的提示词并回复
	if reply, found := prompt_reply.GetPromptReply(message.Text); found {
		// 创建回复消息
		replyMsg := tgbotapi.NewMessage(message.Chat.ID, reply)
		replyMsg.ReplyToMessageID = message.MessageID
		sent, err := bot.Send(replyMsg)
		if err != nil {
			// 发送回复失败时记录错误
			log.Printf("未能发送及时回复: %v", err)
		} else {
			// 3分钟后删除回复消息
			go deleteMessageAfterDelay(bot, message.Chat.ID, sent.MessageID, 3*time.Minute)
		}
	}
}

func RunMessageHandler() error {
	log.Println("消息处理器启动...")

	baseDelay := time.Second
	maxDelay := 5 * time.Minute
	delay := baseDelay
	db, err := core.NewDatabase()
	if err != nil {
		return fmt.Errorf("failed to initialize database: %w", err)
	}
	defer db.Close() // 确保在函数结束时关闭数据库连接

	for {
		err := func() error {
			log.Printf("Attempting to create bot with token: %s", core.BOT_TOKEN)
			bot, err := tgbotapi.NewBotAPI(core.BOT_TOKEN)
			if err != nil {
				log.Printf("Error details: %+v", err)
				return fmt.Errorf("failed to create bot: %w", err)
			}

			bot.Debug = core.DEBUG_MODE

			log.Printf("Authorized on account %s", bot.Self.UserName)

			err = core.RegisterCommands(bot)
			if err != nil {
				return fmt.Errorf("error registering commands: %w", err)
			}

			linkFilter, err := link_filter.NewLinkFilter()
			if err != nil {
				return fmt.Errorf("failed to create LinkFilter: %v", err)
			}

			rateLimiter := core.NewRateLimiter()

			u := tgbotapi.NewUpdate(0)
			u.Timeout = 60

			updates := bot.GetUpdatesChan(u)

			for update := range updates {
				go handleUpdate(bot, update, linkFilter, rateLimiter, db)
			}

			return nil
		}()

		if err != nil {
			log.Printf("Bot encountered an error: %v", err)
			log.Printf("Attempting to restart in %v...", delay)
			time.Sleep(delay)

			delay *= 2
			if delay > maxDelay {
				delay = maxDelay
			}
		} else {
			delay = baseDelay
			log.Println("Bot disconnected. Attempting to restart immediately...")
		}
	}
}

// 下面是辅助函数部分
//
//
//

const (
	maxMessageLength = 4000
)

func deleteMessageAfterDelay(bot *tgbotapi.BotAPI, chatID int64, messageID int, delay time.Duration) {
	go func() {
		time.Sleep(delay)
		deleteMsg := tgbotapi.NewDeleteMessage(chatID, messageID)
		_, err := bot.Request(deleteMsg)
		if err != nil {
			log.Printf("删除消息失败 (ChatID: %d, MessageID: %d): %v", chatID, messageID, err)
		}
	}()
}

func SendLongMessage(bot *tgbotapi.BotAPI, chatID int64, prefix string, items []string) error {
	message := prefix + "\n"
	for i, item := range items {
		newLine := fmt.Sprintf("%d. %s\n", i+1, item)
		if len(message)+len(newLine) > maxMessageLength {
			if err := sendMessage(bot, chatID, message); err != nil {
				return err
			}
			message = ""
		}
		message += newLine
	}

	if message != "" {
		return sendMessage(bot, chatID, message)
	}

	return nil
}

func sendMessage(bot *tgbotapi.BotAPI, chatID int64, text string) error {
	msg := tgbotapi.NewMessage(chatID, text)
	_, err := bot.Send(msg)
	return err
}

func sendErrorMessage(bot *tgbotapi.BotAPI, chatID int64, errMsg string) {
	sendMessage(bot, chatID, errMsg)
}

func HandleKeywordCommand(bot *tgbotapi.BotAPI, message *tgbotapi.Message, command string, args string, db *core.Database) {
	args = strings.TrimSpace(args)

	switch command {
	case "list":
		handleListKeywords(bot, message, db)
	case "add":
		handleAddKeyword(bot, message, args, db)
	case "delete":
		handleDeleteKeyword(bot, message, args, db)
	case "deletecontaining":
		handleDeleteContainingKeyword(bot, message, args, db)
	default:
		sendErrorMessage(bot, message.Chat.ID, "无效的命令或参数。")
	}
}

func handleListKeywords(bot *tgbotapi.BotAPI, message *tgbotapi.Message, db *core.Database) {
	keywords, err := db.GetAllKeywords()
	if err != nil {
		sendErrorMessage(bot, message.Chat.ID, "获取关键词列表时发生错误。")
		return
	}
	if len(keywords) == 0 {
		sendMessage(bot, message.Chat.ID, "关键词列表为空。")
	} else {
		SendLongMessage(bot, message.Chat.ID, "当前关键词列表：", keywords)
	}
}

func handleAddKeyword(bot *tgbotapi.BotAPI, message *tgbotapi.Message, keyword string, db *core.Database) {
	if keyword == "" {
		sendErrorMessage(bot, message.Chat.ID, "请提供要添加的关键词。")
		return
	}

	exists, err := db.KeywordExists(keyword)
	if err != nil {
		sendErrorMessage(bot, message.Chat.ID, "检查关键词时发生错误。")
		return
	}
	if !exists {
		err = db.AddKeyword(keyword)
		if err != nil {
			sendErrorMessage(bot, message.Chat.ID, "添加关键词时发生错误。")
		} else {
			sendMessage(bot, message.Chat.ID, fmt.Sprintf("关键词 '%s' 已添加。", keyword))
		}
	} else {
		sendMessage(bot, message.Chat.ID, fmt.Sprintf("关键词 '%s' 已存在。", keyword))
	}
}

func handleDeleteKeyword(bot *tgbotapi.BotAPI, message *tgbotapi.Message, keyword string, db *core.Database) {
	if keyword == "" {
		sendErrorMessage(bot, message.Chat.ID, "请提供要删除的关键词。")
		return
	}

	err := db.RemoveKeyword(keyword)
	if err != nil {
		sendErrorMessage(bot, message.Chat.ID, fmt.Sprintf("删除关键词 '%s' 时发生错误: %v", keyword, err))
		return
	}

	exists, err := db.KeywordExists(keyword)
	if err != nil {
		sendErrorMessage(bot, message.Chat.ID, fmt.Sprintf("检查关键词 '%s' 是否存在时发生错误: %v", keyword, err))
		return
	}

	if !exists {
		sendMessage(bot, message.Chat.ID, fmt.Sprintf("关键词 '%s' 已成功删除。", keyword))
	} else {
		handleSimilarKeywords(bot, message, keyword, db)
	}
}

func handleSimilarKeywords(bot *tgbotapi.BotAPI, message *tgbotapi.Message, keyword string, db *core.Database) {
	similarKeywords, err := db.SearchKeywords(keyword)
	if err != nil {
		sendErrorMessage(bot, message.Chat.ID, "搜索关键词时发生错误。")
		return
	}
	if len(similarKeywords) > 0 {
		SendLongMessage(bot, message.Chat.ID, fmt.Sprintf("未能删除关键词 '%s'。\n\n以下是相似的关键词：", keyword), similarKeywords)
	} else {
		sendMessage(bot, message.Chat.ID, fmt.Sprintf("未能删除关键词 '%s'，且未找到相似的关键词。", keyword))
	}
}

func handleDeleteContainingKeyword(bot *tgbotapi.BotAPI, message *tgbotapi.Message, substring string, db *core.Database) {
	if substring == "" {
		sendErrorMessage(bot, message.Chat.ID, "请提供要删除的子字符串。")
		return
	}

	removedKeywords, err := db.RemoveKeywordsContaining(substring)
	if err != nil {
		sendErrorMessage(bot, message.Chat.ID, "删除关键词时发生错误。")
		return
	}
	if len(removedKeywords) > 0 {
		SendLongMessage(bot, message.Chat.ID, fmt.Sprintf("已删除包含 '%s' 的以下关键词：", substring), removedKeywords)
	} else {
		sendMessage(bot, message.Chat.ID, fmt.Sprintf("没有找到包含 '%s' 的关键词。", substring))
	}
}

func HandleWhitelistCommand(bot *tgbotapi.BotAPI, message *tgbotapi.Message, command string, args string, db *core.Database) {
	args = strings.TrimSpace(args)

	switch command {
	case "listwhite":
		handleListWhitelist(bot, message, db)
	case "addwhite":
		handleAddWhitelist(bot, message, args, db)
	case "delwhite":
		handleDeleteWhitelist(bot, message, args, db)
	default:
		sendErrorMessage(bot, message.Chat.ID, "无效的命令或参数。")
	}
}

func handleListWhitelist(bot *tgbotapi.BotAPI, message *tgbotapi.Message, db *core.Database) {
	whitelist, err := db.GetAllWhitelist()
	if err != nil {
		sendErrorMessage(bot, message.Chat.ID, fmt.Sprintf("获取白名单时发生错误: %v", err))
		return
	}
	if len(whitelist) == 0 {
		sendMessage(bot, message.Chat.ID, "白名单为空。")
	} else {
		SendLongMessage(bot, message.Chat.ID, "白名单域名列表：", whitelist)
	}
}

func handleAddWhitelist(bot *tgbotapi.BotAPI, message *tgbotapi.Message, domain string, db *core.Database) {
	if domain == "" {
		sendErrorMessage(bot, message.Chat.ID, "请提供要添加的域名。")
		return
	}

	domain = strings.ToLower(domain)
	exists, err := db.WhitelistExists(domain)
	if err != nil {
		sendErrorMessage(bot, message.Chat.ID, fmt.Sprintf("检查白名单时发生错误: %v", err))
		return
	}
	if exists {
		sendMessage(bot, message.Chat.ID, fmt.Sprintf("域名 '%s' 已在白名单中。", domain))
		return
	}

	err = db.AddWhitelist(domain)
	if err != nil {
		sendErrorMessage(bot, message.Chat.ID, fmt.Sprintf("添加到白名单时发生错误: %v", err))
		return
	}

	exists, err = db.WhitelistExists(domain)
	if err != nil {
		sendErrorMessage(bot, message.Chat.ID, fmt.Sprintf("验证添加操作时发生错误: %v", err))
		return
	}
	if exists {
		sendMessage(bot, message.Chat.ID, fmt.Sprintf("域名 '%s' 已成功添加到白名单。", domain))
	} else {
		sendErrorMessage(bot, message.Chat.ID, fmt.Sprintf("未能添加域名 '%s' 到白名单。", domain))
	}
}

func handleDeleteWhitelist(bot *tgbotapi.BotAPI, message *tgbotapi.Message, domain string, db *core.Database) {
	if domain == "" {
		sendErrorMessage(bot, message.Chat.ID, "请提供要删除的域名。")
		return
	}

	domain = strings.ToLower(domain)
	exists, err := db.WhitelistExists(domain)
	if err != nil {
		sendErrorMessage(bot, message.Chat.ID, fmt.Sprintf("检查白名单时发生错误: %v", err))
		return
	}
	if !exists {
		sendMessage(bot, message.Chat.ID, fmt.Sprintf("域名 '%s' 不在白名单中。", domain))
		return
	}

	err = db.RemoveWhitelist(domain)
	if err != nil {
		sendErrorMessage(bot, message.Chat.ID, fmt.Sprintf("从白名单删除时发生错误: %v", err))
		return
	}

	exists, err = db.WhitelistExists(domain)
	if err != nil {
		sendErrorMessage(bot, message.Chat.ID, fmt.Sprintf("验证删除操作时发生错误: %v", err))
		return
	}
	if !exists {
		sendMessage(bot, message.Chat.ID, fmt.Sprintf("域名 '%s' 已成功从白名单中删除。", domain))
	} else {
		sendErrorMessage(bot, message.Chat.ID, fmt.Sprintf("未能从白名单中删除域名 '%s'。", domain))
	}
}
