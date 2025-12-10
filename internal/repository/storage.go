package repository

import (
	"database/sql"
	"santa/internal/model"
)

type Storage struct {
	db *sql.DB
}

func NewStorage(db *sql.DB) *Storage {
	return &Storage{db: db}
}

func (s *Storage) CreateUser(u *model.User) error {
	_, err := s.db.Exec(`
		INSERT INTO users (group_id, tg_id, active, desires, leader)
		VALUES ($1, $2, $3, $4, $5)
	`, u.GroupID, u.TgID, u.Active, u.Desires, u.Leader)
	return err
}

func (s *Storage) UpdateDesires(tgID int64, groupID int64, desires string) error {
	_, err := s.db.Exec(`UPDATE users SET desires=$1 WHERE tg_id=$2 AND group_id=$3`, desires, tgID, groupID)
	return err
}

func (s *Storage) SetActive(tgID int64, groupID int64, active bool) error {
	_, err := s.db.Exec(`UPDATE users SET active=$1 WHERE tg_id=$2 AND group_id=$3`, active, tgID, groupID)
	return err
}

func (s *Storage) ListUsers(groupID int64) ([]model.User, error) {
	rows, err := s.db.Query(`SELECT id, group_id, tg_id, active, desires, leader FROM users WHERE group_id=$1`, groupID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	users := []model.User{}
	for rows.Next() {
		var u model.User
		if err := rows.Scan(&u.ID, &u.GroupID, &u.TgID, &u.Active, &u.Desires, &u.Leader); err != nil {
			return nil, err
		}
		users = append(users, u)
	}
	return users, nil
}

func (s *Storage) IsLeader(tgID int64, groupID int64) (bool, error) {
	var leader bool
	err := s.db.QueryRow(`SELECT leader FROM users WHERE tg_id=$1 AND group_id=$2`, tgID, groupID).Scan(&leader)
	if err != nil {
		return false, err
	}
	return leader, nil
}
