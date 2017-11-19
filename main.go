package main

import (
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/asuleymanov/golos-go/client"
	"github.com/go-telegram-bot-api/telegram-bot-api"

	"github.com/GolosTools/golos-vote-bot/config"
	"github.com/GolosTools/golos-vote-bot/db"
	"github.com/GolosTools/golos-vote-bot/helpers"
	"github.com/GolosTools/golos-vote-bot/models"
)

const (
	buttonAddKey        = "🐬Делегировать"
	buttonRemoveKey     = "🦀Остановить"
	buttonSetPowerLimit = "💪Настройка"
	buttonInformation   = "⚓️Информация"
)

func main() {
	var configuration config.Config
	err := config.LoadConfiguration("./config.json", &configuration)
	if err != nil {
		log.Panic(err)
	}
	err = config.LoadConfiguration("./config.local.json", &configuration)
	if err != nil && !os.IsNotExist(err) {
		log.Panic(err)
	}

	database, err := db.InitDB(configuration.DatabasePath)
	if err != nil {
		if err.Error() == "unable to open database file" {
			path, err := filepath.Abs(configuration.DatabasePath)
			if err != nil {
				log.Panic(err)
			}
			log.Panic(fmt.Sprintf("unable to open database at path: %s", path))
		}
		log.Panic(err)
	}
	defer database.Close()

	bot, err := tgbotapi.NewBotAPI(configuration.TelegramToken)
	if err != nil {
		log.Panic(err)
	}

	bot.Debug = false

	log.Printf("Authorized on account %s", bot.Self.UserName)

	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60

	updates, err := bot.GetUpdatesChan(u)
	if err != nil {
		log.Panic(err)
	}
	for update := range updates {
		err := processMessage(bot, update, configuration, database)
		if err != nil {
			log.Println(err)
		}
	}
}

func processMessage(bot *tgbotapi.BotAPI, update tgbotapi.Update, config config.Config, database *sql.DB) error {
	chatID, err := getChatID(update)
	if err != nil {
		return err
	}
	userID, err := getUserID(update)
	if err != nil {
		return err
	}
	msg := tgbotapi.NewMessage(chatID, "")

	state, err := models.GetStateByUserID(userID, database)
	if err != nil {
		return err
	}

	if update.Message != nil {
		domainList := strings.Join(config.Domains, "|")
		domainRegexp, err := regexp.Compile("https://(?:" + domainList + ")(?:[-a-zA-Z0-9@:%_+.~#?&//=]{2,256})?/@([-a-zA-Z0-9.]{2,256})/([-a-zA-Z0-9@:%_+.~?&=]{2,256})")
		if err != nil {
			return err
		}
		if false == domainRegexp.MatchString(update.Message.Text) && update.Message.Chat.Type != "private" {
			return nil
		}
		switch {
		case update.Message.IsCommand():
			switch update.Message.Command() {
			case "start":
				username := "%username%"
				if len(update.Message.From.FirstName) > 0 {
					username = update.Message.From.FirstName
				}
				msg.Text = fmt.Sprintf("Привет, %s! \n\n"+
					"Я — бот для коллективного кураторства в [социальной блокчейн-сети \"Голос\"](https://golos.io).\n\n"+
					"Мой код полностью открыт и находится здесь: https://github.com/GolosTools/golos-vote-bot\n\n"+
					"Предлагаю начать с нажатия кнопки \""+buttonAddKey+"\", "+
					"после чего я дам ссылку на группу для предложения постов.\n\n"+
					"По любым вопросам пиши моему хозяину — %s",
					username, config.Developer)
				// save referral if exists
				if len(update.Message.CommandArguments()) > 0 {
					_, err := models.GetCredentialByUserID(userID, database)
					if err == sql.ErrNoRows {
						decodedString, err := base64.URLEncoding.DecodeString(update.Message.CommandArguments())
						if err == nil {
							// TODO: проверить существование этого юзера
							referrer := string(decodedString)
							referral := models.Referral{UserID: userID, Referrer: referrer, Completed: false}
							_, err = referral.Save(database)
							if err != nil {
								log.Println("не сохранили реферала: " + err.Error())
							}
						} else {
							log.Printf("не смогли раскодировать строку %s", update.Message.CommandArguments())
						}
					}
				}
			}
			state.Action = update.Message.Command()
		case update.Message.Text == buttonAddKey:
			msg.Text = fmt.Sprintf("Добавь доверенный аккаунт *%s* в https://golos.cf/multi/, "+
				"а затем скажи мне свой логин на Голосе", config.Account)
			state.Action = buttonAddKey
		case update.Message.Text == buttonRemoveKey:
			msg.Text = fmt.Sprintf("Произошла ошибка, свяжись с разработчиком - %s", config.Developer)
			isActive := models.IsActiveCredential(userID, database)
			if isActive {
				credential, err := models.GetCredentialByUserID(userID, database)
				credential.Active = false
				result, err := credential.Save(database)
				if true == result && err == nil {
					msg.Text = "Отлично, я больше не буду использовать твой аккаунт при курировании постов. " +
						"Дополнительно можешь удалить все сторонние ключи из своего аккаунта здесь: " +
						"https://golos.cf/multi/off.html"
				}
			} else {
				msg.Text = "Аккаунт не активирован"
			}
			state.Action = buttonRemoveKey
		case update.Message.Text == buttonSetPowerLimit:
			if false == models.IsActiveCredential(userID, database) {
				msg.Text = "Сначала делегируй мне права кнопкой " + buttonAddKey
				break
			}
			msg.Text = "Введи значение делегируемой силы Голоса от 1 до 100%"
			state.Action = buttonSetPowerLimit
		case update.Message.Text == buttonInformation:
			if false == models.IsActiveCredential(userID, database) {
				msg.Text = "У меня пока нет информации для тебя"
				break
			}
			credential, err := models.GetCredentialByUserID(userID, database)
			if err != nil {
				return err
			}
			encodedUserName := base64.URLEncoding.EncodeToString([]byte(credential.UserName))
			referralLink := "https://t.me/" + config.TelegramBotName + "?start=" + encodedUserName
			msg.Text = fmt.Sprintf("Аккаунт: *%s*\n"+
				"Делегированная сила: *%d%%*\n"+
				"Внутренний рейтинг: *%d пунктов*\n"+
				"Ссылка для приглашения: [%s](%s)\n(в случае успеха дает обоим по %.3f Силы Голоса)",
				credential.UserName, credential.Power, credential.Rating, referralLink, referralLink, config.ReferralFee)
			state.Action = buttonInformation
		case domainRegexp.MatchString(update.Message.Text):
			msg.ReplyToMessageID = update.Message.MessageID

			matched := domainRegexp.FindStringSubmatch(update.Message.Text)
			author, permalink := matched[1], matched[2]

			golos := client.NewApi(config.Rpc, config.Chain)
			defer golos.Rpc.Close()
			post, err := golos.Rpc.Database.GetContent(author, permalink)
			if err != nil {
				return err
			}
			// check post exists in blockchain
			if post.Author != author || post.Permlink != permalink {
				return nil
			}

			if update.Message.Chat.ID != config.GroupID {
				msg.Text = "Удобный просмотр с мобильных устройств:\n" + getInstantViewLink(author, permalink)
				msg.DisableWebPagePreview = false
				bot.Send(msg)
				return nil
			}

			if update.Message.Chat.Type == "private" {
				msg.Text = "Предложить пост можно в нашей группе " + config.GroupLink
				break
			}

			if models.GetTodayVotesCountForUserID(userID, database) >= config.MaximumUserVotesPerDay {
				msg.Text = "Лимит твоих постов на сегодня превышен. Приходи завтра!"
				break
			}

			if models.GetLastVote(database).UserID == userID {
				msg.Text = "Нельзя предлагать два поста подряд. Наберись терпения!"
				break
			}

			if models.GetOpenedVotesCount(database) >= config.MaximumOpenedVotes {
				msg.Text = "Слишком много уже открытых голосований. Может сначала с ними разберёмся?"
				break
			}

			isActive := models.IsActiveCredential(userID, database)
			if false == isActive {
				msg.Text = "Я тебя не знаю и не могу допустить к кураторству. " +
					"Напиши мне в личку, давай обсудим этот вопрос"
				break
			}

			if post.Mode != "first_payout" {
				msg.Text = "Выплата за пост уже была произведена!"
				break
			}

			if post.MaxAcceptedPayout == "0.000 GBG" {
				msg.Text = "Мне не интересно голосовать за пост с отключенными выплатами"
				break
			}

			if helpers.IsVoxPopuli(author) {
				msg.Text = "Сообщества vox-populi могут сами себя поддержать"
				break
			}

			percent := 100

			voteModel := models.Vote{
				UserID:    userID,
				Author:    author,
				Permalink: permalink,
				Percent:   percent,
				Date:      time.Now(),
			}

			if voteModel.Exists(database) {
				msg.Text = "Уже голосовала за этот пост!"
				break
			}

			voteID, err := voteModel.Save(database)
			if err != nil {
				return err
			}

			log.Printf("Вкинули статью \"%s\" автора \"%s\" в чате %d", permalink, author, chatID)

			msg.Text = "Голосование за пост открыто\n" + getInstantViewLink(author, permalink)
			markup := getVoteMarkup(voteID, 0, 0)
			msg.ReplyMarkup = markup
			msg.DisableWebPagePreview = false
			_, err = bot.Send(msg)
			if err != nil {
				return err
			}
			return nil
		case state.Action == buttonAddKey:
			login := strings.ToLower(update.Message.Text)
			login = strings.Trim(login, "@")
			credential := models.Credential{
				UserID:   userID,
				UserName: login,
				Power:    100,
				Rating:   config.InitialUserRating,
				Active:   true,
			}
			if rating, err := credential.GetRating(database); err == nil {
				credential.Rating = rating
			}

			golos := client.NewApi(config.Rpc, config.Chain)
			defer golos.Rpc.Close()
			accounts, err := golos.Rpc.Database.GetAccounts([]string{login})
			if err != nil {
				return err
			} else if len(accounts) == 1 {
				hasPostingAuth := false
				for _, auth := range accounts[0].Posting.AccountAuths {
					if auth.([]interface{})[0] == config.Account {
						hasPostingAuth = true
						break
					}
				}
				if hasPostingAuth {
					// send referral fee
					referral, err := models.GetReferralByUserID(userID, database)
					if err == nil && false == referral.Completed {
						if err = referral.SetCompleted(database); err == nil {
							_, err = models.GetCredentialByUserName(credential.UserName, database)
							if err == sql.ErrNoRows {
								go sendReferralFee(referral.Referrer, credential.UserName, config, bot, database)
							}
						}
					}

					_, err = credential.Save(database)
					if err != nil {
						return err
					}
					msg.Text = "Поздравляю, теперь ты полноправный куратор! " +
						"Присоединяйся к нашей группе для участия в курировании: " + config.GroupLink
					state.Action = "successAuth"
				} else {
					msg.Text = fmt.Sprintf("Доступ у этого аккаунта для меня отсутствует. "+
						"Добавить его можно в https://golos.cf/multi/ для аккаунта *%s*", config.Account)
				}
			} else {
				msg.Text = fmt.Sprintf("Что-то пошло не так. Попробуй повторить позже "+
					"или свяжись с разработчиком: %s", config.Developer)
				log.Printf("Введён некорректный логин: %s", update.Message.Text)
			}
		case state.Action == buttonSetPowerLimit:
			re := regexp.MustCompile("[0-9]+")
			valueString := re.FindString(update.Message.Text)
			value, err := strconv.Atoi(valueString)
			if err != nil {
				msg.Text = "Не поняла. Введи значение делегируемой силы Голоса от 1 до 100%"
				break
			}
			if value >= 1 && value <= 100 {
				if false == models.IsActiveCredential(userID, database) {
					msg.Text = "Сначала делегируй мне права кнопкой " + buttonAddKey
					break
				}
				credential, err := models.GetCredentialByUserID(userID, database)
				if err != nil {
					return err
				}
				err = credential.UpdatePower(value, database)
				if err != nil {
					return err
				}
				msg.Text = fmt.Sprintf("Предоставленная мне в распоряжение сила Голоса "+
					"для аккаунта *%s* теперь равна *%d%%*", credential.UserName, value)
				state.Action = "updatedPower"
			}
		default:
			if update.Message.Chat.Type != "private" {
				return nil
			}
			msg.Text = "Не понимаю"
		}
		if msg.ReplyMarkup == nil && update.Message.Chat.Type == "private" {
			firstButton := tgbotapi.NewKeyboardButton(buttonAddKey)
			secondButton := tgbotapi.NewKeyboardButton(buttonRemoveKey)
			firstButtonRow := []tgbotapi.KeyboardButton{firstButton, secondButton}
			thirdButton := tgbotapi.NewKeyboardButton(buttonSetPowerLimit)
			fourthButton := tgbotapi.NewKeyboardButton(buttonInformation)
			secondButtonRow := []tgbotapi.KeyboardButton{thirdButton, fourthButton}
			keyboard := tgbotapi.NewReplyKeyboard(firstButtonRow, secondButtonRow)
			msg.ReplyMarkup = keyboard
		}
	} else if update.CallbackQuery != nil {
		arr := strings.Split(update.CallbackQuery.Data, "_")
		voteStringID, action := arr[0], arr[1]
		voteID, err := strconv.ParseInt(voteStringID, 10, 64)
		if err != nil {
			return err
		}

		if false == models.IsActiveCredential(userID, database) {
			config := tgbotapi.CallbackConfig{
				CallbackQueryID: update.CallbackQuery.ID,
				Text:            "Я тебя не знаю, не могу допустить к голосованию",
			}
			bot.AnswerCallbackQuery(config)
			return nil
		}

		if models.GetLastResponse(database).UserID == userID {
			config := tgbotapi.CallbackConfig{
				CallbackQueryID: update.CallbackQuery.ID,
				Text:            "Нельзя так часто голосовать",
			}
			bot.AnswerCallbackQuery(config)
			return nil
		}

		voteModel := models.GetVote(database, voteID)
		if voteModel.Completed {
			return nil
		}
		if voteModel.UserID == userID {
			config := tgbotapi.CallbackConfig{
				CallbackQueryID: update.CallbackQuery.ID,
				Text:            "Нельзя голосовать за свой же пост!",
			}
			bot.AnswerCallbackQuery(config)
			return nil
		}

		isGood := action == "good"
		response := models.Response{
			UserID: userID,
			VoteID: voteID,
			Result: isGood,
			Date:   time.Now(),
		}
		text := "Вы уже голосовали!"
		responseExists := response.Exists(database)
		if !responseExists {
			text = "Голос принят"
		}

		credential := models.Credential{UserID: userID}
		rating, err := credential.GetRating(database)
		if err != nil {
			return err
		}
		if rating <= config.RequiredVotes {
			text = "Слишком мало рейтинга для голосования, предлагайте посты"
			config := tgbotapi.CallbackConfig{
				CallbackQueryID: update.CallbackQuery.ID,
				Text:            text,
			}
			bot.AnswerCallbackQuery(config)
			return nil
		}

		callbackConfig := tgbotapi.CallbackConfig{
			CallbackQueryID: update.CallbackQuery.ID,
			Text:            text,
		}
		bot.AnswerCallbackQuery(callbackConfig)

		if !responseExists {
			_, err := response.Save(database)
			if err != nil {
				return err
			}
			voteModel := models.GetVote(database, voteID)
			err = verifyVotes(bot, voteModel, update, config, database)
			if err != nil {
				return err
			}
			// уменьшаем рейтинг голосовавшего при отрциательном голосовании
			if !response.Result {
				credential.DecrementRating(1, database)
			}
		}
		return nil
	}

	_, err = state.Save(database)
	if err != nil {
		return err
	}

	if msg.Text == "" {
		return errors.New("отсутствует текст сообщения")
	}

	msg.ParseMode = "Markdown"
	msg.DisableWebPagePreview = true
	_, err = bot.Send(msg)
	if err != nil {
		return err
	}
	return nil
}

func verifyVotes(bot *tgbotapi.BotAPI, voteModel models.Vote, update tgbotapi.Update, config config.Config, database *sql.DB) error {
	chatID, err := getChatID(update)
	if err != nil {
		return err
	}
	userID, err := getUserID(update)
	if err != nil {
		return err
	}
	messageID, err := getMessageID(update)
	if err != nil {
		return err
	}

	responses, err := models.GetAllResponsesForVoteID(voteModel.VoteID, database)
	if err != nil {
		return err
	}

	var positives, negatives int
	for _, response := range responses {
		if response.Result {
			positives = positives + 1
		} else {
			negatives = negatives + 1
		}
	}

	markup := getVoteMarkup(voteModel.VoteID, positives, negatives)
	updateTextConfig := tgbotapi.EditMessageTextConfig{
		BaseEdit: tgbotapi.BaseEdit{
			ChatID:      chatID,
			MessageID:   messageID,
			ReplyMarkup: &markup,
		},
		Text: update.CallbackQuery.Message.Text,
	}
	bot.Send(updateTextConfig)

	credential := models.Credential{UserID: userID}

	if positives+negatives >= config.RequiredVotes {
		if voteModel.Completed {
			return nil
		}
		voteModel.Completed = true
		_, err := voteModel.Save(database)
		if err != nil {
			return err
		}
		msg := tgbotapi.NewEditMessageText(chatID, messageID, "")
		if positives >= negatives {
			credential.IncrementRating(1, database)
			successVotes := vote(voteModel, config, database)
			msg.Text = fmt.Sprintf("Успешно проголосовала c %d аккаунтов", successVotes)
		} else {
			credential.DecrementRating(2*config.RequiredVotes, database)
			rating, err := credential.GetRating(database)
			if err != nil {
				return err
			}
			msg.Text = "Пост отклонен, рейтинг предлагающего снижен"
			if rating < 0 {
				err = removeUser(bot, chatID, userID)
				if err != nil {
					log.Println(err)
					msg.Text = "Пост отклонен, предлагающий должен быть исключен"
				} else {
					msg.Text = "Пост отклонен, предлагающий исключен"
				}
			}
			// восстанавливаем рейтинг кураторам
			for _, response := range responses {
				// которые отклонили пост
				if false == response.Result {
					credential, err := models.GetCredentialByUserID(response.UserID, database)
					if err != nil {
						return err
					}
					err = credential.IncrementRating(1, database)
					if err != nil {
						return err
					}
				}
			}
		}
		_, err = bot.Send(msg)
		if err != nil {
			return err
		}
	}
	return nil
}

func removeUser(bot *tgbotapi.BotAPI, chatID int64, userID int) error {
	memberConfig := tgbotapi.KickChatMemberConfig{
		ChatMemberConfig: tgbotapi.ChatMemberConfig{
			ChatID: chatID,
			UserID: userID,
		},
		UntilDate: 0,
	}
	_, err := bot.KickChatMember(memberConfig)
	return err
}

func vote(vote models.Vote, config config.Config, database *sql.DB) int {
	credentials, err := models.GetAllCredentials(database)
	if err != nil {
		log.Println("Не смогли извлечь ключи из базы")
		return 0
	}
	for _, credential := range credentials {
		client.Key_List[credential.UserName] = client.Keys{PKey: config.PostingKey}
	}
	log.Printf("Загружено %d аккаунтов", len(credentials))

	var errors []error
	var wg sync.WaitGroup
	wg.Add(len(credentials))
	for _, credential := range credentials {
		client.Key_List[credential.UserName] = client.Keys{PKey: config.PostingKey}
		go func(credential models.Credential) {
			defer wg.Done()
			weight := vote.Percent * 100
			golos := client.NewApi(config.Rpc, config.Chain)
			defer golos.Rpc.Close()
			err := golos.Vote(credential.UserName, vote.Author, vote.Permalink, weight)
			if err != nil {
				log.Println("Ошибка при голосовании: " + err.Error())
				errors = append(errors, err)
			}
		}(credential)
	}
	wg.Wait()
	return len(credentials) - len(errors)
}

func getVoteMarkup(voteID int64, positives int, negatives int) tgbotapi.InlineKeyboardMarkup {
	stringVoteID := strconv.FormatInt(voteID, 10)
	goodButton := tgbotapi.NewInlineKeyboardButtonData("👍Лайк ("+strconv.Itoa(positives)+")", stringVoteID+"_good")
	badButton := tgbotapi.NewInlineKeyboardButtonData("👎Дизлайк ("+strconv.Itoa(negatives)+")", stringVoteID+"_bad")
	row := []tgbotapi.InlineKeyboardButton{badButton, goodButton}
	markup := tgbotapi.InlineKeyboardMarkup{}
	markup.InlineKeyboard = append(markup.InlineKeyboard, row)
	return markup
}

func getChatID(update tgbotapi.Update) (int64, error) {
	if update.Message != nil {
		return update.Message.Chat.ID, nil
	} else if update.CallbackQuery != nil {
		return update.CallbackQuery.Message.Chat.ID, nil
	} else {
		return 0, errors.New("не получили ID чата")
	}
}

func getUserID(update tgbotapi.Update) (int, error) {
	if update.Message != nil {
		return update.Message.From.ID, nil
	} else if update.CallbackQuery != nil {
		return update.CallbackQuery.From.ID, nil
	} else {
		return 0, errors.New("не получили ID пользователя")
	}
}

func getMessageID(update tgbotapi.Update) (int, error) {
	if update.Message != nil {
		return update.Message.MessageID, nil
	} else if update.CallbackQuery != nil {
		return update.CallbackQuery.Message.MessageID, nil
	} else {
		return 0, errors.New("не получили ID сообщения")
	}
}

func getInstantViewLink(author string, permalink string) string {
	return "https://t.me/iv?url=https://goldvoice.club/" + "@" + author + "/" + permalink + "&rhash=70f46c6616076d"
}

func sendReferralFee(referrer string, referral string, config config.Config, bot *tgbotapi.BotAPI, database *sql.DB) {
	golos := client.NewApi(config.Rpc, config.Chain)
	defer golos.Rpc.Close()
	client.Key_List[config.Account] = client.Keys{AKey: config.ActiveKey}
	amount := fmt.Sprintf("%.3f GOLOS", config.ReferralFee)
	err := golos.TransferToVesting(config.Account, referrer, amount)
	err2 := golos.TransferToVesting(config.Account, referral, amount)
	if err != nil {
		log.Println(fmt.Sprintf("Не отправили силу голоса %s \nаккаунту %s", err.Error(), referrer))
	}
	if err2 != nil {
		log.Println(fmt.Sprintf("Не отправили силу голоса %s \nаккаунту %s", err.Error(), referral))
	}
	if err != nil || err2 != nil {
		return
	}
	markdownLink := func(account string) string {
		return fmt.Sprintf("[@%s](https://golos.io/@%s/transfers)", account, account)
	}
	referrerLink := markdownLink(referrer)
	referralLink := markdownLink(referral)
	text := fmt.Sprintf("Пригласивший %s и приглашённый %s получают по %.3f Силы Голоса в рамках партнёрской программы",
		referrerLink, referralLink, config.ReferralFee)
	msg := tgbotapi.NewMessage(config.GroupID, text)
	msg.ParseMode = "Markdown"
	_, err = bot.Send(msg)
	if err != nil {
		log.Println("Не отправили сообщение: " + text)
	}
}
