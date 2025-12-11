package telegram

import (
	tg "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

func MainButtons() tg.ReplyKeyboardMarkup {
	return tg.NewReplyKeyboard(
		tg.NewKeyboardButtonRow(
			tg.NewKeyboardButton("🏠 Главное меню"),
		),
	)
}

func StartButtons() tg.ReplyKeyboardMarkup {
	return tg.NewReplyKeyboard(
		tg.NewKeyboardButtonRow(
			tg.NewKeyboardButton("👨‍💼 Я организатор"),
			tg.NewKeyboardButton("👥 Я участник"),
		),
	)
}

func OrganizerMenu() tg.ReplyKeyboardMarkup {
	return tg.NewReplyKeyboard(
		tg.NewKeyboardButtonRow(
			tg.NewKeyboardButton("🆕 Новый Тайный Санта"),
		),
		tg.NewKeyboardButtonRow(
			tg.NewKeyboardButton("🔄 Я уже Тайный Санта"),
		),
		tg.NewKeyboardButtonRow(
			tg.NewKeyboardButton("🏠 Главное меню"),
		),
	)
}

func OrganizerGroupMenu() tg.ReplyKeyboardMarkup {
	return tg.NewReplyKeyboard(
		tg.NewKeyboardButtonRow(
			tg.NewKeyboardButton("Показать участников"),
		),
		tg.NewKeyboardButtonRow(
			tg.NewKeyboardButton("Отправить пожелания"),
		),
		tg.NewKeyboardButtonRow(
			tg.NewKeyboardButton("Отказаться от участия"),
		),
		tg.NewKeyboardButtonRow(
			tg.NewKeyboardButton("Запустить Тайного Санту!"),
		),
	)
}
