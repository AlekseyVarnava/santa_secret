package telegram

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"math/rand"
	"net/http"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"

	"santa/internal/fsm"
)

// handlers.go — основной файл с логикой бота.

// Bot хранит основное состояние и зависимости.
type Bot struct {
	api    *tgbotapi.BotAPI
	db     *sql.DB
	log    *zap.Logger
	fsm    *fsm.FSM
	rnd    *rand.Rand
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	// pendingGroup хранит временную привязку пользователя -> group_id
	pendingGroupMu sync.Mutex
	pendingGroup   map[int64]int64
}

// NewBot создаёт новый экземпляр бота. Принимает существующее подключение к БД, токен и логгер.
func NewBot(db *sql.DB, token string, logger *zap.Logger) (*Bot, error) {
	if db == nil {
		return nil, errors.New("db is nil")
	}
	if token == "" {
		return nil, errors.New("bot token empty")
	}

	client := &http.Client{
		Timeout: 30 * time.Second,
	}

	api, err := tgbotapi.NewBotAPI(token)
	if err != nil {
		return nil, err
	}

	api.Client = client

	ctx, cancel := context.WithCancel(context.Background())

	b := &Bot{
		api:          api,
		db:           db,
		log:          logger,
		fsm:          fsm.NewFSM(),
		rnd:          rand.New(rand.NewSource(time.Now().UnixNano())),
		ctx:          ctx,
		cancel:       cancel,
		pendingGroup: make(map[int64]int64),
	}

	return b, nil
}

// Run запускает постоянный цикл обработки апдейтов (polling).
func (b *Bot) Run() {
	b.log.Info("bot run: starting updates listener")
	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60
	updates := b.api.GetUpdatesChan(u)

	for {
		select {
		case <-b.ctx.Done():
			b.log.Info("bot run: context done, shutting down")
			b.wg.Wait()
			return
		case upd := <-updates:
			// Обрабатываем каждый update в собственной горутине (высокая параллельность)
			b.wg.Add(1)
			go func(update tgbotapi.Update) {
				defer b.wg.Done()
				if update.Message != nil {
					if err := b.handleMessage(update.Message); err != nil {
						b.log.Error("handleMessage", zap.Error(err))
					}
				} else if update.CallbackQuery != nil {
					if err := b.handleCallback(update.CallbackQuery); err != nil {
						b.log.Error("handleCallback", zap.Error(err))
					}
				}
			}(upd)
		}
	}
}

// Stop — graceful shutdown
func (b *Bot) Stop() {
	b.cancel()
}

// -------------------- Обработчики --------------------

func (b *Bot) handleMessage(msg *tgbotapi.Message) error {
	chatID := msg.Chat.ID
	userID := msg.From.ID
	text := strings.TrimSpace(msg.Text)

	// Логируем минимальную информацию
	b.log.Debug("incoming message", zap.Int64("chat_id", chatID), zap.Int64("from_id", userID), zap.String("text", text))

	// Команды
	if msg.IsCommand() {
		switch msg.Command() {
		case "start":
			return b.cmdStart(chatID, userID)
		case "run_group":
			// /run_group CODE
			arg := msg.CommandArguments()
			if arg == "" {
				return b.sendText(chatID, "Usage: /run_group <GROUP_CODE>")
			}
			return b.cmdRunGroup(chatID, userID, strings.TrimSpace(arg))
		case "create_group":
			return b.cmdCreateGroup(chatID, userID)
		default:
			return b.sendText(chatID, "Неизвестная команда")
		}
	}

	// FSM-based routing: если у пользователя установлен ожидающий state, меняем поведение
	st := b.fsm.Get(userID)
	switch st {
	case fsm.StateOrgGroupID:
		// Организатор ввёл код группы, показать меню для этой группы
		return b.handleOrganizerGroupCode(chatID, userID, text)
	case fsm.StateEnterGroupID:
		// Ожидали код группы для присоединения
		code := text
		return b.handleJoinByCode(chatID, userID, code)
	case fsm.StateEnterDesires, fsm.StateOrgEnterDes:
		return b.handleReceiveDesires(chatID, userID, text)
	}

	// Если простое меню
	switch text {
	case "Главное меню":
		return b.sendStartMenu(chatID)
	case "Я организатор":
		b.fsm.Set(userID, fsm.StateOrgMenu)
		return b.showOrganizerMenu(chatID)
	case "Я участник":
		b.fsm.Set(userID, fsm.StateEnterGroupID)
		return b.sendText(chatID, "Введите код группы, который вам дал организатор:")
	case "Новый Тайный Санта":
		return b.createGroupFlow(chatID, userID)
	case "Я уже Тайный Санта":
		b.fsm.Set(userID, fsm.StateOrgGroupID)
		return b.sendText(chatID, "Введите код группы:")
	case "Показать участников":
		return b.handleShowParticipants(chatID, userID)
	case "Отправить пожелания":
		b.fsm.Set(userID, fsm.StateOrgEnterDes)
		return b.sendText(chatID, "Отправьте ваши пожелания одним сообщением:")
	case "Отказаться от участия":
		return b.handleDecline(chatID, userID)
	case "Запустить Тайного Санту!":
		return b.initiateStartSanta(chatID, userID)
	}

	// По умолчанию — показываем стартовое меню
	return b.sendStartMenu(chatID)
}

func (b *Bot) handleCallback(q *tgbotapi.CallbackQuery) error {
	// Убираем "часики"
	cb := tgbotapi.NewCallback(q.ID, "")
	if _, err := b.api.Request(cb); err != nil {
		b.log.Warn("callback answer failed", zap.Error(err))
	}

	data := q.Data
	chatID := q.Message.Chat.ID
	uid := q.From.ID

	b.log.Debug("callback", zap.String("data", data), zap.Int64("chat_id", chatID))

	switch {
	case data == "MAIN_MENU":
		return b.sendStartMenu(chatID)

	case strings.HasPrefix(data, "JOIN:"):
		code := strings.TrimPrefix(data, "JOIN:")
		return b.handleJoinByCode(chatID, uid, code)

	case strings.HasPrefix(data, "SENDMSG:"):
		code := strings.TrimPrefix(data, "SENDMSG:")
		return b.sendAssignmentsToParticipants(code)

	case strings.HasPrefix(data, "ORG_WILL_PARTICIPATE:"):
		code := strings.TrimPrefix(data, "ORG_WILL_PARTICIPATE:")
		return b.handleOrgWillParticipateCallback(chatID, uid, code)

	case strings.HasPrefix(data, "ORG_WONT_PARTICIPATE:"):
		// просто показать меню организатора
		return b.showOrganizerMenu(chatID)

	case strings.HasPrefix(data, "SHOW_PARTICIPANTS:"):
		code := strings.TrimPrefix(data, "SHOW_PARTICIPANTS:")
		return b.handleShowParticipantsForGroup(chatID, uid, code)

	case strings.HasPrefix(data, "ORG_SEND_DESIRES:"):
		code := strings.TrimPrefix(data, "ORG_SEND_DESIRES:")
		return b.handleOrgSendDesiresCallback(chatID, uid, code)

	case strings.HasPrefix(data, "DECLINE_FOR_GROUP:"):
		code := strings.TrimPrefix(data, "DECLINE_FOR_GROUP:")
		return b.handleDeclineForGroup(chatID, uid, code)

	case strings.HasPrefix(data, "START_FOR_GROUP:"):
		code := strings.TrimPrefix(data, "START_FOR_GROUP:")
		return b.cmdRunGroup(chatID, uid, code)

	default:
		// нераспознанный callback
		b.log.Debug("unknown callback", zap.String("data", data))
		return nil
	}
}

// -------------------- Команды и потоки --------------------

func (b *Bot) cmdStart(chatID int64, userID int64) error {
	b.fsm.Set(userID, fsm.StateAskRole)
	msg := "Привет! Тут нужно тебе придумать новогоднее обращение интересное, и нужно спросить, человек организатор тайного санты или участник"
	m := tgbotapi.NewMessage(chatID, msg)
	m.ReplyMarkup = StartButtons()
	_, err := b.api.Send(m)
	return err
}

func (b *Bot) sendStartMenu(chatID int64) error {
	m := tgbotapi.NewMessage(chatID, "Главное меню")
	m.ReplyMarkup = StartButtons()
	_, err := b.api.Send(m)
	return err
}

func (b *Bot) cmdCreateGroup(chatID int64, userID int64) error {
	return b.createGroupFlow(chatID, userID)
}

// createGroupFlow — создание новой группы и добавление организатора в participants
func (b *Bot) createGroupFlow(chatID int64, userID int64) error {
	code := b.generateGroupCode()
	// create group and participant in transaction
	tx, err := b.db.Begin()
	if err != nil {
		b.log.Error("createGroupFlow: begin tx", zap.Error(err))
		return b.sendText(chatID, "Ошибка БД при создании группы")
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	var groupID int64
	err = tx.QueryRow(`INSERT INTO santa_groups (group_code, leader_tg_id) VALUES ($1, $2) RETURNING id`, code, userID).Scan(&groupID)
	if err != nil {
		b.log.Error("createGroupFlow: insert group", zap.Error(err))
		return b.sendText(chatID, "Ошибка при создании группы")
	}
	_, err = tx.Exec(`INSERT INTO participants (group_id, tg_id, active, desires, leader) VALUES ($1,$2,true,'',true)`, groupID, userID)
	if err != nil {
		b.log.Error("createGroupFlow: insert participant", zap.Error(err))
		return b.sendText(chatID, "Ошибка при добавлении организатора")
	}
	if err := tx.Commit(); err != nil {
		b.log.Error("createGroupFlow: commit", zap.Error(err))
		return b.sendText(chatID, "Ошибка при создании группы")
	}

	// Спросим, будет ли участвовать организатор
	text := fmt.Sprintf("Создана новая группа Тайного Санты.\nКод группы: %s\nПоделитесь кодом с друзьями.\nВы хотите участвовать?", code)
	m := tgbotapi.NewMessage(chatID, text)
	m.ReplyMarkup = tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("Буду участвовать", "ORG_WILL_PARTICIPATE:"+code),
			tgbotapi.NewInlineKeyboardButtonData("Не буду участвовать", "ORG_WONT_PARTICIPATE:"+code),
		),
	)
	_, err = b.api.Send(m)
	return err
}

func (b *Bot) generateGroupCode() string {
	const letters = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	n := 6
	bts := make([]byte, n)
	for i := range bts {
		bts[i] = letters[b.rnd.Intn(len(letters))]
	}
	return string(bts)
}

// handleJoinByCode — добавляет пользователя в группу с кодом
func (b *Bot) handleJoinByCode(chatID int64, userID int64, code string) error {
	code = strings.TrimSpace(code)
	var groupID int64
	err := b.db.QueryRow(`SELECT id FROM santa_groups WHERE group_code=$1`, code).Scan(&groupID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return b.sendText(chatID, "Группа с таким кодом не найдена")
		}
		b.log.Error("handleJoinByCode: select group", zap.Error(err))
		return b.sendText(chatID, "Ошибка при поиске группы")
	}

	_, err = b.db.Exec(`INSERT INTO participants (group_id, tg_id, active, desires, leader) VALUES ($1,$2,true,'',false) ON CONFLICT (group_id,tg_id) DO UPDATE SET active=true`, groupID, userID)
	if err != nil {
		b.log.Error("handleJoinByCode: insert participant", zap.Error(err))
		return b.sendText(chatID, "Ошибка при добавлении в группу")
	}

	// Просим отправить пожелания
	// Просим отправить пожелания
	b.pendingGroupMu.Lock()
	b.pendingGroup[userID] = groupID
	b.pendingGroupMu.Unlock()

	b.fsm.Set(userID, fsm.StateEnterDesires)

	m := tgbotapi.NewMessage(chatID, "Вы присоединились. Отправьте, пожалуйста, ваши пожелания для подарка одним сообщением.")
	m.ReplyMarkup = tgbotapi.NewRemoveKeyboard(true)
	_, err = b.api.Send(m)
	return err
}

func (b *Bot) handleOrganizerGroupCode(chatID int64, userID int64, code string) error {
	code = strings.TrimSpace(code)
	var groupID int64
	var leaderID int64
	if err := b.db.QueryRow(`SELECT id, leader_tg_id FROM santa_groups WHERE group_code=$1`, code).Scan(&groupID, &leaderID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return b.sendText(chatID, "Группа не найдена")
		}
		b.log.Error("handleOrganizerGroupCode: select", zap.Error(err))
		return b.sendText(chatID, "Ошибка при поиске группы")
	}
	if leaderID != userID {
		return b.sendText(chatID, "Вы не являетесь организатором этой группы")
	}

	// показываем меню для конкретной группы (используем inline кнопки с кодом)
	m := tgbotapi.NewMessage(chatID, fmt.Sprintf("Меню для группы %s", code))
	m.ReplyMarkup = tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("Показать участников", "SHOW_PARTICIPANTS:"+code),
			tgbotapi.NewInlineKeyboardButtonData("Отправить пожелания", "ORG_SEND_DESIRES:"+code),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("Отказаться от участника (по коду)", "DECLINE_FOR_GROUP:"+code),
			tgbotapi.NewInlineKeyboardButtonData("Запустить Тайного Санту!", "START_FOR_GROUP:"+code),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("Главное меню", "MAIN_MENU"),
		),
	)
	_, err := b.api.Send(m)
	// Сбрасываем FSM — остаёмся в роли организатора
	b.fsm.Set(userID, fsm.StateOrgMenu)
	return err
}

// handleReceiveDesires — сохранение пожеланий для participant'а; работает и для организатора
func (b *Bot) handleReceiveDesires(chatID int64, userID int64, text string) error {
	// Проверяем, есть ли pendingGroup для данного пользователя
	b.pendingGroupMu.Lock()
	gid, has := b.pendingGroup[userID]
	if has {
		delete(b.pendingGroup, userID)
	}
	b.pendingGroupMu.Unlock()

	var affected int64

	if has {
		// Сохраняем только для указанной группы
		if _, err := b.db.Exec(`UPDATE participants SET desires=$1 WHERE tg_id=$2 AND group_id=$3`, text, userID, gid); err != nil {
			b.log.Error("handleReceiveDesires: update single group", zap.Error(err))
			return b.sendText(chatID, "Ошибка при сохранении пожеланий")
		}
		affected = 1
	} else {
		// Старое поведение: обновить для всех групп пользователя
		rows, err := b.db.Query(`SELECT group_id FROM participants WHERE tg_id=$1`, userID)
		if err != nil {
			b.log.Error("handleReceiveDesires: select group ids", zap.Error(err))
			return b.sendText(chatID, "Ошибка при сохранении пожеланий")
		}
		defer rows.Close()

		for rows.Next() {
			var gid2 int64
			if err := rows.Scan(&gid2); err != nil {
				continue
			}
			if _, err := b.db.Exec(`UPDATE participants SET desires=$1 WHERE tg_id=$2 AND group_id=$3`, text, userID, gid2); err == nil {
				affected++
			}
		}
	}

	// Возвращаем пользователя в главное меню/ролевое состояние
	b.fsm.Set(userID, fsm.StateAskRole)
	return b.sendText(chatID, fmt.Sprintf("Пожелания сохранены для %d группы(ы)", affected))
}

// handleShowParticipants — показываем список участников для группы, где текущий пользователь лидер
func (b *Bot) handleShowParticipants(chatID int64, userID int64) error {
	// Найдём группы, где пользователь лидер
	rows, err := b.db.Query(`SELECT g.group_code, p.tg_id, p.desires, p.active FROM participants p JOIN santa_groups g ON g.id=p.group_id WHERE g.leader_tg_id=$1 ORDER BY p.id`, userID)
	if err != nil {
		b.log.Error("handleShowParticipants: query", zap.Error(err))
		return b.sendText(chatID, "Ошибка при получении участников")
	}
	defer rows.Close()

	var sb strings.Builder
	var any bool
	for rows.Next() {
		any = true
		var code string
		var tg int64
		var desires string
		var active bool
		if err := rows.Scan(&code, &tg, &desires, &active); err != nil {
			continue
		}
		status := "❌"
		if active {
			status = "✅"
		}
		sb.WriteString(fmt.Sprintf("Группа %s — @%d — %s — пожелания: %s\n", code, tg, status, desires))
	}
	if !any {
		return b.sendText(chatID, "Вы не являетесь организатором ни одной группы или нет участников.")
	}
	return b.sendText(chatID, sb.String())
}

// handleDecline — пользователь отписывается от всех групп (active=false)
func (b *Bot) handleDecline(chatID int64, userID int64) error {
	res, err := b.db.Exec(`UPDATE participants SET active=false WHERE tg_id=$1`, userID)
	if err != nil {
		b.log.Error("handleDecline: update", zap.Error(err))
		return b.sendText(chatID, "Ошибка при отказе от участия")
	}
	n, _ := res.RowsAffected()
	return b.sendText(chatID, fmt.Sprintf("Вы отказались от участия в %d группах", n))
}

// handleOrgWillParticipateCallback — организатор нажал \"Буду участвовать\"
func (b *Bot) handleOrgWillParticipateCallback(chatID int64, userID int64, code string) error {
	// Найти группу и group_id
	var groupID int64
	if err := b.db.QueryRow(`SELECT id FROM santa_groups WHERE group_code=$1`, code).Scan(&groupID); err != nil {
		b.log.Error("handleOrgWillParticipate: select group", zap.Error(err))
		return b.sendText(chatID, "Группа не найдена")
	}

	// Убедимся, что участник (организатор) есть в participants — если нет, добавим
	_, err := b.db.Exec(`INSERT INTO participants (group_id, tg_id, active, desires, leader) VALUES ($1,$2,true,'',true) ON CONFLICT (group_id,tg_id) DO UPDATE SET active=true, leader=TRUE`, groupID, userID)
	if err != nil {
		b.log.Error("handleOrgWillParticipate: upsert participant", zap.Error(err))
		return b.sendText(chatID, "Ошибка при регистрации участия")
	}

	// Связываем pendingGroup, чтобы следующий ввод пожеланий записался только в эту группу
	b.pendingGroupMu.Lock()
	b.pendingGroup[userID] = groupID
	b.pendingGroupMu.Unlock()

	// Перевести FSM в состояние ввода пожеланий (специальное состояние для организатора)
	b.fsm.Set(userID, fsm.StateOrgEnterDes)

	return b.sendText(chatID, "Отлично — пришлите одним сообщением ваши пожелания для подарка (они будут привязаны к этой группе).")
}

// handleOrgSendDesiresCallback — организатор нажал кнопку \"Отправить пожелания\" для конкретной группы
func (b *Bot) handleOrgSendDesiresCallback(chatID int64, userID int64, code string) error {
	var groupID int64
	if err := b.db.QueryRow(`SELECT id FROM santa_groups WHERE group_code=$1`, code).Scan(&groupID); err != nil {
		b.log.Error("handleOrgSendDesiresCallback: select group", zap.Error(err))
		return b.sendText(chatID, "Группа не найдена")
	}
	// проверка права — пользователь должен быть в участниках или лидер
	var exists bool
	if err := b.db.QueryRow(`SELECT true FROM participants WHERE group_id=$1 AND tg_id=$2`, groupID, userID).Scan(&exists); err != nil {
		// если нет — добавим как обычного участника (но не делаем лидером)
		_, err2 := b.db.Exec(`INSERT INTO participants (group_id, tg_id, active, desires, leader) VALUES ($1,$2,true,'',false) ON CONFLICT DO NOTHING`, groupID, userID)
		if err2 != nil {
			b.log.Error("handleOrgSendDesiresCallback: insert participant", zap.Error(err2))
			return b.sendText(chatID, "Ошибка при регистрации в группе")
		}
	}

	b.pendingGroupMu.Lock()
	b.pendingGroup[userID] = groupID
	b.pendingGroupMu.Unlock()
	b.fsm.Set(userID, fsm.StateOrgEnterDes)
	return b.sendText(chatID, "Отправьте ваши пожелания для этой группы одним сообщением.")
}

func (b *Bot) handleShowParticipantsForGroup(chatID int64, userID int64, code string) error {
	var groupID int64
	var leaderID int64
	if err := b.db.QueryRow(`SELECT id, leader_tg_id FROM santa_groups WHERE group_code=$1`, code).Scan(&groupID, &leaderID); err != nil {
		b.log.Error("handleShowParticipantsForGroup: select group", zap.Error(err))
		return b.sendText(chatID, "Группа не найдена")
	}
	if leaderID != userID {
		return b.sendText(chatID, "Вы не являетесь организатором этой группы")
	}

	rows, err := b.db.Query(`SELECT tg_id, desires, active FROM participants WHERE group_id=$1 ORDER BY id`, groupID)
	if err != nil {
		b.log.Error("handleShowParticipantsForGroup: query", zap.Error(err))
		return b.sendText(chatID, "Ошибка при получении участников")
	}
	defer rows.Close()

	var sb strings.Builder
	var any bool
	for rows.Next() {
		any = true
		var tg int64
		var desires sql.NullString
		var active bool
		if err := rows.Scan(&tg, &desires, &active); err != nil {
			continue
		}
		status := "❌"
		if active {
			status = "✅"
		}
		d := desires.String
		if d == "" {
			d = "(пожеланий нет)"
		}
		sb.WriteString(fmt.Sprintf("@%d — %s — %s\n", tg, status, d))
	}
	if !any {
		return b.sendText(chatID, "В группе нет участников.")
	}
	return b.sendText(chatID, sb.String())
}

func (b *Bot) handleDeclineForGroup(chatID int64, userID int64, code string) error {
	// это действие подписано на callback у организатора: он может отключить чьё-то участие?
	// Но по ТЗ: участник должен иметь кнопку отказаться сам. Здесь мы реализуем отказ текущего пользователя в указанной группе.
	var groupID int64
	if err := b.db.QueryRow(`SELECT id FROM santa_groups WHERE group_code=$1`, code).Scan(&groupID); err != nil {
		b.log.Error("handleDeclineForGroup: select group", zap.Error(err))
		return b.sendText(chatID, "Группа не найдена")
	}
	// Отключаем текущего пользователя в этой группе
	if _, err := b.db.Exec(`UPDATE participants SET active=false WHERE group_id=$1 AND tg_id=$2`, groupID, userID); err != nil {
		b.log.Error("handleDeclineForGroup: update", zap.Error(err))
		return b.sendText(chatID, "Ошибка при отказе от участия")
	}
	return b.sendText(chatID, "Вы отказались от участия в этой группе")
}

// initiateStartSanta — инициирует процесс распределения (лидер вводит код группы в диалоге)
func (b *Bot) initiateStartSanta(chatID int64, userID int64) error {
	b.fsm.Set(userID, fsm.StateOrgStartSanta)
	return b.sendText(chatID, "Введите код группы, для которой хотите запустить Тайного Санту (или используйте /run_group <CODE>):")
}

type p struct {
	tg int64
	d  string
}

// cmdRunGroup — обработка команды /run_group <CODE>
func (b *Bot) cmdRunGroup(chatID int64, userID int64, code string) error {
	// Проверим является ли пользователь лидером этой группы
	var groupID int64
	var leaderID int64
	if err := b.db.QueryRow(`SELECT id, leader_tg_id FROM santa_groups WHERE group_code=$1`, code).Scan(&groupID, &leaderID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return b.sendText(chatID, "Группа не найдена")
		}
		b.log.Error("cmdRunGroup: select group", zap.Error(err))
		return b.sendText(chatID, "Ошибка при поиске группы")
	}
	if leaderID != userID {
		return b.sendText(chatID, "Запуск доступен только организатору (лидеру) группы")
	}

	// Получаем активных участников
	rows, err := b.db.Query(`SELECT tg_id, desires FROM participants WHERE group_id=$1 AND active=true`, groupID)
	if err != nil {
		b.log.Error("cmdRunGroup: select participants", zap.Error(err))
		return b.sendText(chatID, "Ошибка при получении участников")
	}
	defer rows.Close()

	var parts []p
	for rows.Next() {
		var pp p
		if err := rows.Scan(&pp.tg, &pp.d); err != nil {
			continue
		}
		parts = append(parts, pp)
	}

	if len(parts) < 2 {
		return b.sendText(chatID, "Недостаточно участников для розыгрыша (минимум 2)")
	}

	assign := b.generateAssignments(parts)

	// Сохраняем assignments
	tx, err := b.db.Begin()
	if err != nil {
		b.log.Error("cmdRunGroup: begin tx", zap.Error(err))
		return b.sendText(chatID, "Ошибка при старте транзакции")
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()
	_, err = tx.Exec(`DELETE FROM assignments WHERE group_id=$1`, groupID)
	if err != nil {
		b.log.Error("cmdRunGroup: delete assignments", zap.Error(err))
		return b.sendText(chatID, "Ошибка БД при очистке предыдущих назначений")
	}
	stmt, err := tx.Prepare(`INSERT INTO assignments (group_id, giver_tg_id, receiver_tg_id) VALUES ($1,$2,$3)`) // nolint
	if err != nil {
		b.log.Error("cmdRunGroup: prepare insert", zap.Error(err))
		return b.sendText(chatID, "Ошибка БД")
	}
	for g, r := range assign {
		if _, err := stmt.Exec(groupID, g, r); err != nil {
			b.log.Error("cmdRunGroup: insert assign", zap.Error(err))
			_ = stmt.Close()
			return b.sendText(chatID, "Ошибка при сохранении назначений")
		}
	}
	_ = stmt.Close()
	if err := tx.Commit(); err != nil {
		b.log.Error("cmdRunGroup: commit", zap.Error(err))
		return b.sendText(chatID, "Ошибка при сохранении назначений")
	}

	// Спросим разрешение на рассылку
	m := tgbotapi.NewMessage(chatID, fmt.Sprintf("Распределение завершено для %d участников. Отправить уведомления участникам?", len(parts)))
	m.ReplyMarkup = tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("Отправить уведомления", "SENDMSG:"+code),
			tgbotapi.NewInlineKeyboardButtonData("Не отправлять", "MAIN_MENU"),
		),
	)
	_, err = b.api.Send(m)
	return err
}

// generateAssignments — генерация назначений избегает self-подарков и двусторонних пар при возможности
func (b *Bot) generateAssignments(parts []p) map[int64]int64 {
	n := len(parts)
	givers := make([]int64, n)
	receivers := make([]int64, n)
	for i := 0; i < n; i++ {
		givers[i] = parts[i].tg
		receivers[i] = parts[i].tg
	}

	// Попробуем случайные перестановки
	tryLimit := 2000
	for t := 0; t < tryLimit; t++ {
		b.rnd.Shuffle(n, func(i, j int) { receivers[i], receivers[j] = receivers[j], receivers[i] })
		valid := true
		for i := 0; i < n; i++ {
			if givers[i] == receivers[i] {
				valid = false
				break
			}
		}
		if !valid {
			continue
		}
		// mutual check
		rev := map[int64]int64{}
		for i := 0; i < n; i++ {
			rev[givers[i]] = receivers[i]
		}
		mutual := false
		for i := 0; i < n; i++ {
			a := givers[i]
			b_ := receivers[i]
			if rev[b_] == a {
				mutual = true
				break
			}
		}
		if mutual {
			continue
		}
		// ok
		res := map[int64]int64{}
		for i := 0; i < n; i++ {
			res[givers[i]] = receivers[i]
		}
		return res
	}

	// fallback: cyclic shift
	res := make(map[int64]int64, n)
	for i := 0; i < n; i++ {
		res[givers[i]] = givers[(i+1)%n]
	}
	return res
}

// sendAssignmentsToParticipants — рассылает результаты всем участникам для группы с code
func (b *Bot) sendAssignmentsToParticipants(code string) error {
	var groupID int64
	if err := b.db.QueryRow(`SELECT id FROM santa_groups WHERE group_code=$1`, code).Scan(&groupID); err != nil {
		b.log.Error("sendAssignmentsToParticipants: select group", zap.Error(err))
		return b.sendTextToLeaderOfCode(code, "Не удалось найти группу для отправки")
	}

	rows, err := b.db.Query(`SELECT giver_tg_id, receiver_tg_id FROM assignments WHERE group_id=$1`, groupID)
	if err != nil {
		b.log.Error("sendAssignmentsToParticipants: select assignments", zap.Error(err))
		return b.sendTextToLeaderOfCode(code, "Ошибка при чтении назначений")
	}
	defer rows.Close()

	type pr struct {
		g int64
		r int64
	}
	var pairs []pr
	for rows.Next() {
		var p pr
		if err := rows.Scan(&p.g, &p.r); err != nil {
			continue
		}
		pairs = append(pairs, p)
	}

	// Получаем желания receivers
	desires := map[int64]string{}
	for _, p := range pairs {
		if _, ok := desires[p.r]; ok {
			continue
		}
		var d sql.NullString
		if err := b.db.QueryRow(`SELECT desires FROM participants WHERE group_id=$1 AND tg_id=$2`, groupID, p.r).Scan(&d); err != nil {
			desires[p.r] = ""
		} else {
			desires[p.r] = d.String
		}
	}

	// Параллельная отправка с семафором
	sem := make(chan struct{}, 8)
	var wg sync.WaitGroup
	var firstErr error
	var mu sync.Mutex
	for _, p := range pairs {
		wg.Add(1)
		sem <- struct{}{}
		go func(pair pr) {
			defer wg.Done()
			defer func() { <-sem }()
			text := fmt.Sprintf("Вам нужно подарить: @%d\nПожелания: %s", pair.r, desires[pair.r])
			msg := tgbotapi.NewMessage(pair.g, text)
			if _, err := b.api.Send(msg); err != nil {
				mu.Lock()
				if firstErr == nil {
					firstErr = err
				}
				mu.Unlock()
				b.log.Warn("failed to send message to participant", zap.Int64("to", pair.g), zap.Error(err))
			}
		}(p)
	}
	wg.Wait()
	if firstErr != nil {
		return firstErr
	}
	// уведомим лидера, что рассылка завершена
	return b.sendTextToLeaderOfCode(code, "Уведомления отправлены всем участникам")
}

func (b *Bot) sendTextToLeaderOfCode(code, text string) error {
	var leader int64
	if err := b.db.QueryRow(`SELECT leader_tg_id FROM santa_groups WHERE group_code=$1`, code).Scan(&leader); err != nil {
		b.log.Error("sendTextToLeaderOfCode: select leader", zap.Error(err))
		return err
	}
	return b.sendText(leader, text)
}

// sendText — helper
func (b *Bot) sendText(chatID int64, text string) error {
	m := tgbotapi.NewMessage(chatID, text)
	_, err := b.api.Send(m)
	return err
}

// -------------------- Доп. хелперы --------------------

// showOrganizerMenu — простой реплай с пунктами
func (b *Bot) showOrganizerMenu(chatID int64) error {
	m := tgbotapi.NewMessage(chatID, "Главное меню организатора")
	m.ReplyMarkup = OrganizerMenu()
	_, err := b.api.Send(m)
	return err
}

// sendStartMessageTo ensures an initial message is shown when bot added (convenience)
func (b *Bot) sendStartMessageTo(chatID int64) error {
	m := tgbotapi.NewMessage(chatID, "Добро пожаловать в Тайного Санту!")
	m.ReplyMarkup = StartButtons()
	_, err := b.api.Send(m)
	return err
}

// -------------------- SQL миграции подсказка --------------------

/*
Требуемые таблицы:

CREATE TABLE IF NOT EXISTS santa_groups (
    id SERIAL PRIMARY KEY,
    group_code TEXT UNIQUE NOT NULL,
    leader_tg_id BIGINT NOT NULL,
    created_at TIMESTAMPTZ DEFAULT now()
);

CREATE TABLE IF NOT EXISTS participants (
    id SERIAL PRIMARY KEY,
    group_id INT NOT NULL REFERENCES santa_groups(id) ON DELETE CASCADE,
    tg_id BIGINT NOT NULL,
    active BOOLEAN NOT NULL DEFAULT TRUE,
    desires TEXT DEFAULT '',
    leader BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMPTZ DEFAULT now(),
    UNIQUE(group_id, tg_id)
);

CREATE TABLE IF NOT EXISTS assignments (
    id SERIAL PRIMARY KEY,
    group_id INT NOT NULL REFERENCES santa_groups(id) ON DELETE CASCADE,
    giver_tg_id BIGINT NOT NULL,
    receiver_tg_id BIGINT NOT NULL,
    created_at TIMESTAMPTZ DEFAULT now()
);

*/
